package main

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// GetNodeStatuses reports node and PDB status and lists OpenShift resources.
func GetNodeStatuses(ctx context.Context) ([]string, error) {
	// Use the same local kubeconfig search paths as oc/kubectl, without
	// attempting in-cluster authentication.
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeconfig, err := loadingRules.Load()
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes config: %w", err)
	}
	config, err := clientcmd.NewNonInteractiveClientConfig(
		*kubeconfig, kubeconfig.CurrentContext, &clientcmd.ConfigOverrides{}, loadingRules,
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("configure Kubernetes client from %v (check your oc login and current context): %w", loadingRules.GetLoadingPrecedence(), err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list Kubernetes nodes: %w", err)
	}

	statuses := make([]string, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady {
				ready = condition.Status == corev1.ConditionTrue
				break
			}
		}

		status := "NotReady"
		if ready {
			status = "Ready"
		}
		statuses = append(statuses, fmt.Sprintf("%s: %s", node.Name, status))
	}

	// NamespaceAll lists policy/v1 PodDisruptionBudgets across all namespaces.
	pdbs, err := clientset.PolicyV1().PodDisruptionBudgets(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list PodDisruptionBudgets: %w", err)
	}

	for _, pdb := range pdbs.Items {
		status := fmt.Sprintf("%d disruptions allowed", pdb.Status.DisruptionsAllowed)
		if pdb.Status.DisruptionsAllowed == 0 {
			status = "WARNING: no disruptions allowed"
		}
		statuses = append(statuses, fmt.Sprintf("PDB %s/%s: %s", pdb.Namespace, pdb.Name, status))
	}
	if len(pdbs.Items) == 0 {
		statuses = append(statuses, "No PodDisruptionBudgets found")
	}

	// The dynamic client accesses OpenShift resources without extra Go types.
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	clusterOperatorResource := schema.GroupVersionResource{
		Group: "config.openshift.io", Version: "v1", Resource: "clusteroperators",
	}
	operators, err := dynamicClient.Resource(clusterOperatorResource).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list ClusterOperators: %w", err)
	}
	for _, operator := range operators.Items {
		statuses = append(statuses, fmt.Sprintf("ClusterOperator: %s", operator.GetName()))
	}
	if len(operators.Items) == 0 {
		statuses = append(statuses, "No ClusterOperators found")
	}

	apiRequestCountResource := schema.GroupVersionResource{
		Group: "apiserver.openshift.io", Version: "v1", Resource: "apirequestcounts",
	}
	requestCounts, err := dynamicClient.Resource(apiRequestCountResource).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list APIRequestCounts: %w", err)
	}
	for _, requestCount := range requestCounts.Items {
		statuses = append(statuses, fmt.Sprintf("APIRequestCount: %s", requestCount.GetName()))
	}
	if len(requestCounts.Items) == 0 {
		statuses = append(statuses, "No APIRequestCounts found")
	}

	return statuses, nil
}
