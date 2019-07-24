package client

import (
	iamV1alpha1 "github.com/uswitch/kiam/pkg/k8s/client/clientset_generated/clientset"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClient returns an in-cluster or out of cluster clientset depending on
// whether kubecfg is set or empty.
func NewClient(kubecfg string) (*iamV1alpha1.Clientset, error) {
	if kubecfg != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubecfg)
		if err != nil {
			return nil, err
		}
		return iamV1alpha1.NewForConfig(config)
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return iamV1alpha1.NewForConfig(config)
}
