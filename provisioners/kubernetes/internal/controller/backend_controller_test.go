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

var _ = Describe("Backend Controller", func() {
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
		backend := &flockv1alpha1.Backend{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Backend")
			err := k8sClient.Get(ctx, typeNamespacedName, backend)
			if err != nil && errors.IsNotFound(err) {
				resource := &flockv1alpha1.Backend{
					Name:      resourceName,
					Namespace: resourceNamespace,
					Spec: flockv1alpha1.BackendSpec{
						DNS:         &flockv1alpha1.DNS{Host: "backend.example.com", Port: 443},
						HealthCheck: flockv1alpha1.HealthCheck{NoHealthCheck: &flockv1alpha1.NoHealthCheck{}},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &flockv1alpha1.Backend{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Backend")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &BackendReconciler{
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
