//go:build integration_hypershift

package packageoperator

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	k8sscheme "k8s.io/client-go/kubernetes/scheme"

	"k8s.io/apimachinery/pkg/runtime"

	"package-operator.run/apis"

	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"pkg.package-operator.run/cardboard/kubeutils/wait"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	corev1alpha1 "package-operator.run/apis/core/v1alpha1"
)

func TestHyperShift(t *testing.T) {
	namespace := "default-pko-hs-hc"
	ctx := logr.NewContext(context.Background(), testr.New(t))

	require.NoError(t, initClients(ctx))

	rpPkg := &corev1alpha1.Package{}
	err := Client.Get(ctx, client.ObjectKey{Name: "remote-phase", Namespace: namespace}, rpPkg)
	require.NoError(t, err)

	// Wait for roll-out of remote phase package
	// longer timeout because PKO is restarting to enable HyperShift integration and needs a
	// few seconds for leader election.
	err = Waiter.WaitForCondition(
		ctx, rpPkg, corev1alpha1.PackageAvailable,
		metav1.ConditionTrue, wait.WithTimeout(300*time.Second),
	)
	require.NoError(t, err)

	pkgImage := rpPkg.Spec.Image

	hClient, hWaiter, err := hostedClusterHandlers()
	require.NoError(t, err)

	// Test ObjectSetPhase integration
	t.Run("ObjectSetSetupPauseTeardown", func(t *testing.T) {
		runObjectSetSetupPauseTeardownTestWithCustomHandlers(t, hClient, hWaiter, namespace, "hosted-cluster")
	})
	t.Run("ObjectSetHandover", func(t *testing.T) {
		runObjectSetHandoverTestWithCustomHandlers(t, hClient, hWaiter, namespace, "hosted-cluster")
	})
	t.Run("ObjectSetOrphanCascadeDeletion", func(t *testing.T) {
		t.SkipNow() // This test/functionality is not stable.
		runObjectSetOrphanCascadeDeletionTestWithCustomHandlers(t, hClient, hWaiter, namespace, "hosted-cluster")
	})

	t.Run("SubcomponentTolerationsAffinity", func(t *testing.T) {
		type RemotePhasePkgConfig struct {
			Affinity    corev1.Affinity     `json:"affinity"`
			Tolerations []corev1.Toleration `json:"tolerations"`
		}

		type PkoPkgConfig struct {
			SubcomponentAffinity    corev1.Affinity     `json:"subcomponentAffinity"`
			SubcomponentTolerations []corev1.Toleration `json:"subcomponentTolerations"`
		}

		// Get ClusterPackage/package-operator.spec.config.subcomponent{Tolerations,Affinity}
		pkoPkg := &corev1alpha1.ClusterPackage{}
		require.NoError(t, Client.Get(ctx, client.ObjectKey{Name: "package-operator"}, pkoPkg))
		rootCfg := &PkoPkgConfig{}
		require.NoError(t, json.Unmarshal(pkoPkg.Spec.Config.Raw, rootCfg))

		// and validate their propagation to Package/remote-phase.spec.config.{tolerations,affinity}.
		subCfg := &RemotePhasePkgConfig{}
		require.NoError(t, json.Unmarshal(rpPkg.Spec.Config.Raw, subCfg))
		assert.True(t, reflect.DeepEqual(rootCfg.SubcomponentAffinity, subCfg.Affinity))
		assert.True(t, reflect.DeepEqual(rootCfg.SubcomponentTolerations, subCfg.Tolerations))

		// Validate propagation to the remote-phase deployment oject.
		deployment := &appsv1.Deployment{}
		require.NoError(t, Client.Get(ctx,
			client.ObjectKey{
				Name: "package-operator-remote-phase-manager", Namespace: namespace,
			},
			deployment,
		))
		require.NotNil(t, subCfg.Affinity.NodeAffinity)
		assert.True(t, reflect.DeepEqual(subCfg.Affinity.NodeAffinity, deployment.Spec.Template.Spec.Affinity.NodeAffinity))
		assert.True(t, reflect.DeepEqual(subCfg.Tolerations, deployment.Spec.Template.Spec.Tolerations))
	})

	t.Run("HostedClusterComponent", func(t *testing.T) {
		hcPkg := &corev1alpha1.Package{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hosted-cluster",
				Namespace: namespace,
			},
			Spec: corev1alpha1.PackageSpec{
				Image:     pkgImage,
				Component: "hosted-cluster",
			},
		}

		require.NoError(t, Client.Create(ctx, hcPkg))

		// Wait for roll-out of hosted cluster package
		err = Waiter.WaitForCondition(
			ctx, hcPkg, corev1alpha1.PackageAvailable,
			metav1.ConditionTrue, wait.WithTimeout(100*time.Second),
		)
		require.NoError(t, err)
	})
}

func hostedClusterHandlers() (client.Client, *wait.Waiter, error) {
	scheme := runtime.NewScheme()
	schemeBuilder := runtime.SchemeBuilder{
		k8sscheme.AddToScheme,
		apis.AddToScheme,
	}

	if err := schemeBuilder.AddToScheme(scheme); err != nil {
		return nil, nil, fmt.Errorf("adding defaults to scheme: %w", err)
	}

	kubeconfigPath := filepath.Join("..", "..", ".cache", "clusters", "pko-hs-hc", "kubeconfig.yaml")
	// Create RestConfig
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("getting rest.Config from kubeconfig: %w", err)
	}

	// Create Controller Runtime Client
	ctrlClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, nil, fmt.Errorf("creating new ctrl client: %w", err)
	}

	waiter := wait.NewWaiter(ctrlClient, scheme)
	return ctrlClient, waiter, nil
}
