package e2e

import (
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func invokeOLS(env *OLSTestEnvironment, secret *corev1.Secret, query string, expected_success bool) {
	reqBody := []byte(`{"query": ` + query + `"}`)
	resp, body, err := TestHTTPSQueryEndpoint(env, secret, reqBody)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	Expect(resp.StatusCode == http.StatusOK).To(Equal(expected_success))
	fmt.Println(string(body))
}

func shutdownPostgres(env *OLSTestEnvironment) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PostgresDeploymentName,
			Namespace: OLSNameSpace,
		},
	}
	err := env.Client.Get(deployment)
	Expect(err).NotTo(HaveOccurred())
	deployment.Spec.Replicas = Ptr(int32(0))
	err = env.Client.Update(deployment)
	Expect(err).NotTo(HaveOccurred())
	err = env.Client.WaitForDeploymentCondition(deployment, func(dep *appsv1.Deployment) (bool, error) {
		if deployment.Spec.Replicas != Ptr(int32(0)) {
			return false, fmt.Errorf("replica count on the Postgres deployment is still not 0 but %d", *deployment.Spec.Replicas)
		}
		return true, nil
	})
	Expect(err).NotTo(HaveOccurred())
}

func startPostgres(env *OLSTestEnvironment) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PostgresDeploymentName,
			Namespace: OLSNameSpace,
		},
	}
	err := env.Client.Get(deployment)
	Expect(err).NotTo(HaveOccurred())
	deployment.Spec.Replicas = Ptr(int32(1))
	err = env.Client.Update(deployment)
	Expect(err).NotTo(HaveOccurred())
	err = env.Client.WaitForDeploymentRollout(deployment)
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("Postgres restart", Ordered, Label("Postgres restart"), func() {
	var env *OLSTestEnvironment
	var err error

	BeforeAll(func() {
		By("Setting up OLS test environment")
		env, err = SetupOLSTestEnvironment(nil)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		By("Cleaning up OLS test environment with CR deletion")
		err = CleanupOLSTestEnvironmentWithCRDeletion(env, "postgres_restart_test")
		Expect(err).NotTo(HaveOccurred())
	})

	It("should bounce Postgres and reestablish connection with it", func() {
		By("Testing OLS service activation")
		secret, err := TestOLSServiceActivation(env)
		Expect(err).NotTo(HaveOccurred())

		By("Testing HTTPS POST on /v1/query endpoint by OLS user")
		invokeOLS(env, secret, "how do I stop a VM?", true)

		By("shut down Postgres")
		shutdownPostgres(env)

		By("Testing HTTPS POST on /v1/query endpoint by OLS user - should fail")
		invokeOLS(env, secret, "how do I stop a VM?", false)

		By("bring Postgres back up")
		startPostgres(env)

		By("Testing HTTPS POST on /v1/query endpoint by OLS user")
		invokeOLS(env, secret, "how do I stop a VM?", true)
	})
})
