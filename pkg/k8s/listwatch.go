package k8s

import (
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	iamV1Alpha1 "github.com/uswitch/kiam/pkg/k8s/client/clientset_generated/clientset"
)

const (
	// ResourcePods are Pod resources
	ResourcePods = "pods"
	// ResourceNamespaces are Namespace resources
	ResourceNamespaces = "namespaces"
	// ResourceIamRoles are Role resources
	ResourceIamRoles = "iam.amazonaws.com/iamroles"
)

// NewCoreV1ListWatch creates a ListWatch for the specified Resource
func NewCoreV1ListWatch(client *kubernetes.Clientset, resource string) *cache.ListWatch {
	return cache.NewListWatchFromClient(client.CoreV1().RESTClient(), resource, "", fields.Everything())
}

// NewIamV1Alpha1ListWatch creates a ListWatch for the specified Resource
func NewIamV1Alpha1ListWatch(client *iamV1Alpha1.Clientset, resource string) *cache.ListWatch {
	return cache.NewListWatchFromClient(client.RESTClient(), resource, "", fields.Everything())
}
