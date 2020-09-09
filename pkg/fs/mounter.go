package fs

import (
	"os"
	"path/filepath"

	"k8s.io/utils/exec"
	"k8s.io/utils/mount"
)

type MountOptions int

const (
	MountOptionsDefault MountOptions = iota
	MountOptionsBind
)

type Mounter struct {
	base *mount.SafeFormatAndMount
}

func NewMounter() *Mounter {
	base := &mount.SafeFormatAndMount{
		Interface: mount.New(""),
		Exec:      exec.New(),
	}

	return &Mounter{
		base: base,
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
	var err error

	if opts&MountOptionsBind != 0 {
		err = os.MkdirAll(filepath.Dir(target), 0750)
		if err != nil {
			return err
		}

		file, err := os.OpenFile(target, os.O_CREATE, 0660)
		if err != nil {
			return err
		}
		_ = file.Close()

		options = append(options, "bind")
	} else {
		err = os.MkdirAll(target, 0750)
		if err != nil {
			return err
		}
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
