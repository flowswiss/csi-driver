package fs

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/kubernetes/pkg/util/resizefs"
	"k8s.io/utils/exec"
	"k8s.io/utils/mount"
)

type MountOptions int

const (
	MountOptionsDefault MountOptions = iota
	MountOptionsBind
	MountOptionsBlock
)

type Mounter struct {
	base   *mount.SafeFormatAndMount
	resize *resizefs.ResizeFs
}

func NewMounter() *Mounter {
	base := &mount.SafeFormatAndMount{
		Interface: mount.New(""),
		Exec:      exec.New(),
	}

	resize := resizefs.NewResizeFs(base)

	return &Mounter{
		base:   base,
		resize: resize,
	}
}

func (m *Mounter) IsMounted(target string) (bool, error) {
	mountPoints, err := m.base.List()
	if err != nil {
		return false, err
	}

	for _, mountPoint := range mountPoints {
		if mountPoint.Path == target {
			return true, nil
		}
	}

	return false, nil
}

func (m *Mounter) Mount(source, target, fsType string, opts MountOptions) error {
	var options []string

	err := m.createTarget(target, opts)
	if err != nil {
		return err
	}

	if opts&MountOptionsBind != 0 {
		options = append(options, "bind")
	}

	err = m.base.Mount(source, target, fsType, options)
	if err != nil {
		return err
	}

	return nil
}

func (m *Mounter) FormatAndMount(source, target, fsType string, opts MountOptions) error {
	var options []string

	err := m.createTarget(target, opts)
	if err != nil {
		return err
	}

	if opts&MountOptionsBind != 0 {
		options = append(options, "bind")
	}

	err = m.base.FormatAndMount(source, target, fsType, options)
	if err != nil {
		return err
	}

	return nil
}

func (m *Mounter) Unmount(target string) error {
	return m.base.Unmount(target)
}

func (m *Mounter) Resize(path string) error {
	device, err := m.FindDevice(path)
	if err != nil {
		return err
	}

	_, err = m.resize.Resize(device, path)
	if err != nil {
		return err
	}

	return nil
}

func (m *Mounter) FindDevice(path string) (string, error) {
	mountPoint, err := m.FindMountPoint(path)
	if err != nil {
		return "", err
	}

	return mountPoint.Device, nil
}

func (m *Mounter) FindMountPoint(path string) (mount.MountPoint, error) {
	mountPoints, err := m.base.List()
	if err != nil {
		return mount.MountPoint{}, err
	}

	for _, mountPoint := range mountPoints {
		if mountPoint.Path == path {
			return mountPoint, nil
		}
	}

	return mount.MountPoint{}, fmt.Errorf("%s is not mounted", path)
}

func (m *Mounter) createTarget(target string, opts MountOptions) error {
	if opts&MountOptionsBlock != 0 {
		err := os.MkdirAll(filepath.Dir(target), 0750)
		if err != nil {
			return err
		}

		file, err := os.OpenFile(target, os.O_CREATE, 0660)
		if err != nil {
			return err
		}
		_ = file.Close()
	} else {
		err := os.MkdirAll(target, 0750)
		if err != nil {
			return err
		}
	}

	return nil
}
