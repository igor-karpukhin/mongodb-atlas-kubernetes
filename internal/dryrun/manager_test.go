package dryrun

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

type mockCluster struct {
	cluster.Cluster
	cache.Cache

	startErr               error
	waitForCacheSyncResult bool
}

func (c *mockCluster) GetCache() cache.Cache {
	return c
}

func (c *mockCluster) WaitForCacheSync(context.Context) bool {
	return c.waitForCacheSyncResult
}

func (c *mockCluster) Start(ctx context.Context) error {
	<-ctx.Done() // block until context is cancelled
	return c.startErr
}

func TestManagerStart(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		startErr               error
		waitForCacheSyncResult bool
		wantErr                string
	}{
		{
			name:                   "no start error but cache sync failed",
			startErr:               nil,
			waitForCacheSyncResult: false,
			wantErr:                "cluster cache sync failed",
		},
		{
			name:                   "cache sync error is preferred over start error",
			startErr:               errors.New("start error"),
			waitForCacheSyncResult: false,
			wantErr:                "cluster cache sync failed",
		},
		{
			name:                   "start error",
			startErr:               errors.New("start error"),
			waitForCacheSyncResult: true,
			wantErr:                "cluster start failed: start error",
		},
		{
			name:                   "no errors",
			startErr:               nil,
			waitForCacheSyncResult: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(&mockCluster{
				startErr:               tc.startErr,
				waitForCacheSyncResult: tc.waitForCacheSyncResult,
			}, zaptest.NewLogger(t))
			gotErr := ""
			if err := m.Start(context.Background()); err != nil {
				gotErr = err.Error()
			}
			require.Equal(t, tc.wantErr, gotErr)
		})
	}
}
