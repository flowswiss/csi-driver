package driver

import (
	"context"

	"golang.org/x/sync/errgroup"
	"k8s.io/klog"

	"github.com/flowswiss/goclient/flow"
)

type HealthCheck interface {
	Check(ctx context.Context) error
}

type HealthChecker struct {
	checks []HealthCheck
}

func (c *HealthChecker) Check(ctx context.Context) error {
	group := errgroup.Group{}

	for _, check := range c.checks {
		group.Go(func() error {
			return check.Check(ctx)
		})
	}

	return group.Wait()
}

type apiAccessHealthCheck struct {
	client *flow.Client
}

func (a *apiAccessHealthCheck) Check(ctx context.Context) error {
	klog.V(2).Info("Probing api access health check")

	_, _, err := a.client.Server.List(ctx, flow.PaginationOptions{PerPage: 1})
	if err != nil {
		return err
	}

	return nil
}
