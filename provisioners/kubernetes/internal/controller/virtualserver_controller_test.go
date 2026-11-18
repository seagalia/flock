//go:build envtest

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	flockv1alpha1 "github.com/seagalia/flock/provisioners/kubernetes/api/v1alpha1"
)

var _ = Describe("VirtualServer Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}
		virtualserver := &flockv1alpha1.VirtualServer{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind VirtualServer")
			err := k8sClient.Get(ctx, typeNamespacedName, virtualserver)
			if err != nil && errors.IsNotFound(err) {
				resource := &flockv1alpha1.VirtualServer{
					Name:      resourceName,
					Namespace: resourceNamespace,
					Spec: flockv1alpha1.VirtualServerSpec{
						Port:           443,
						TCPRouteConfig: &flockv1alpha1.TCPRouteConfig{BackendRef: flockv1alpha1.ObjectRef{Name: "app"}},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &flockv1alpha1.VirtualServer{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance VirtualServer")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &VirtualServerReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
