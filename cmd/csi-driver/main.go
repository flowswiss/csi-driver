package main

import (
	"flag"
	"os"

	"k8s.io/klog"

	"github.com/flowswiss/csi-driver/pkg/driver"
)

var (
	token    = flag.String("token", "", "the application token to authorize with the flow api")
	baseUrl  = flag.String("base-url", "https://api.flow.swiss/", "the api base url to use for all api requests")
	hostname = flag.String("hostname", "", "the name of the current node where the driver is running on")
	socket   = flag.String("socket", "unix:///tmp/csi.sock", "the address on which the driver should listen on")
)

func main() {
	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	if token == nil || *token == "" {
		klog.Error("no api token provided")
		os.Exit(1)
	}

	if hostname == nil || *hostname == "" {
		klog.Error("no hostname provided")
		os.Exit(1)
	}

	d, err := driver.NewDriver(*token, *baseUrl, *hostname)
	if err != nil {
		klog.Error(err)
		os.Exit(1)
	}

	err = d.Listen(*socket)
	if err != nil {
		klog.Error(err)
		os.Exit(1)
	}
}
