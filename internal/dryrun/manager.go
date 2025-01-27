package dryrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler interface {
	reconcile.Reconciler
	For() (client.Object, builder.Predicates)
}

// Manager is a controller-runtime runnable
// that acts similar to controller-runtime's Manager
// but executing dry-run functionality.
type Manager struct {
	cluster.Cluster
	wg          sync.WaitGroup
	startOnce   sync.Once
	reconcilers []Reconciler
	logger      *zap.Logger
}

func NewManager(c cluster.Cluster, logger *zap.Logger) *Manager {
	return &Manager{
		Cluster: c,
		logger:  logger.Named("dry-run-manager"),
	}
}

func (m *Manager) SetupReconciler(r Reconciler) {
	m.reconcilers = append(m.reconcilers, r)
}

func (m *Manager) executeDryRun(ctx context.Context) error {
	if !m.Cluster.GetCache().WaitForCacheSync(ctx) {
		return errors.New("cluster cache sync failed")
	}

	for _, reconciler := range m.reconcilers {
		originalResource, _ := reconciler.For()
		resource := originalResource.DeepCopyObject() // don't mutate the prototype

		// build GVK
		if resource.GetObjectKind().GroupVersionKind().Empty() {
			gvks, _, err := m.Cluster.GetScheme().ObjectKinds(resource)
			if err != nil {
				return fmt.Errorf("unable to determine GVK for resource %T: %w", resource, err)
			}
			if len(gvks) == 0 {
				return fmt.Errorf("no GVKs present for resource %T", resource)
			}
			objectKind, ok := resource.(schema.ObjectKind)
			if !ok {
				return fmt.Errorf("unable to set GVK for resource %T: %w", resource, err)
			}
			objectKind.SetGroupVersionKind(gvks[len(gvks)-1]) // set the latest version, it's what our local specs follow
		}

		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(resource.GetObjectKind().GroupVersionKind())

		if err := m.Cluster.GetClient().List(ctx, list); err != nil {
			return fmt.Errorf("unable to list resources: %w", err)
		}

		for _, item := range list.Items {
			req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&item)}
			if _, err := reconciler.Reconcile(ctx, req); err != nil {
				return fmt.Errorf("unable to reconcile item %v: %w", item, err)
			}
		}
	}
	return nil
}

// Start executes the dry-run and returns immediately.
// In contrast to controller-runtime's Manager it doesn't periodically reconcile
// but exits after the dry-run pass.
//
// This method blocks until the dry-run is complete
func (m *Manager) Start(ctx context.Context) error {
	var (
		wg         sync.WaitGroup
		clusterErr error
	)

	cancelCtx, stopCluster := context.WithCancel(ctx)
	wg.Add(1)
	go func() {
		defer wg.Done()
		// this blocks until it errors out or the context is cancelled
		// where we instruct the Cluster to stop.
		if err := m.Cluster.Start(cancelCtx); err != nil {
			clusterErr = fmt.Errorf("cluster start failed: %w", err)
		}
	}()

	if dryRunErr := m.executeDryRun(cancelCtx); dryRunErr != nil {
		stopCluster() // opportunistically stop the Cluster object.
		return dryRunErr
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      os.Getenv("JOB_NAME"),
			Namespace: os.Getenv("JOB_NAMESPACE"),
		},
	}
	if job.Name == "" || job.Namespace == "" {
		m.logger.Warn("dry run finished: not running as a Job, skipping event emission")
	} else {
		m.Cluster.GetEventRecorderFor("DryRun Manager").Event(job, corev1.EventTypeNormal, DryRunReason, "dry run finished")
	}

	stopCluster()
	wg.Wait()
	return clusterErr
}
