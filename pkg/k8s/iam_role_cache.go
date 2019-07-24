// Copyright 2017 uSwitch
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package k8s

import (
	"context"
	"fmt"
	"time"

	"sort"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/uswitch/kiam/pkg/apis/iam/v1alpha1"
	iamV1alpha1client "github.com/uswitch/kiam/pkg/k8s/client/clientset_generated/clientset"
	informers "github.com/uswitch/kiam/pkg/k8s/client/informers_generated/externalversions"
	v1alpha1Listers "github.com/uswitch/kiam/pkg/k8s/client/listers_generated/iam/v1alpha1"
	authv1 "k8s.io/api/authorization/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// IamRoleCache implements a cache, allowing lookups by their IP address
type IamRoleCache struct {
	iamRoles     chan *v1alpha1.IamRole
	informer     cache.SharedIndexInformer
	lister       v1alpha1Listers.IamRoleLister
	corev1Client *kubernetes.Clientset
}

// NewPodCache creates the cache object that uses a watcher to listen for Pod events. The cache indexes pods by their
// IP address so that Kiam can identify which role a Pod should assume. It periodically syncs the list of
// pods and can announce Pods. When announcing Pods via the channel it will drop events if the buffer
// is full- bufferSize determines how many.
func NewIamRoleCache(client iamV1alpha1client.Interface, corev1Client *kubernetes.Clientset, syncInterval time.Duration, bufferSize int) *IamRoleCache {
	informerFactory := informers.NewSharedInformerFactory(client, syncInterval)
	informer := informerFactory.Iam().V1alpha1().IamRoles().Informer()
	lister := informerFactory.Iam().V1alpha1().IamRoles().Lister()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    OnAdd,
		DeleteFunc: OnDelete,
		UpdateFunc: OnUpdate,
	})

	iamRoleCache := &IamRoleCache{
		informer:     informer,
		lister:       lister,
		corev1Client: corev1Client,
	}

	return iamRoleCache
}

// FindIamRole finds the IAM role by it's namespace and name
func (c *IamRoleCache) FindIamRole(ctx context.Context, namespace string, name string) (*v1alpha1.IamRole, error) {
	obj, exists, err := c.informer.GetIndexer().GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	return obj.(*v1alpha1.IamRole), nil
}

var (
	// ErrPodNotFound is returned when there's no matching Pod in the cache.
	ErrRoleNotFound = fmt.Errorf("role not found")
)

// Run starts the controller processing updates. Blocks until the cache has synced
func (s *IamRoleCache) Run(ctx context.Context) error {
	go s.informer.Run(ctx.Done())
	log.Infof("started cache controller")

	ok := cache.WaitForCacheSync(ctx.Done(), s.informer.HasSynced)
	if !ok {
		return ErrWaitingForSync
	}

	return nil
}

// computeSecurityContext derives a valid security context while trying to avoid any changes to the given pod. I.e.
// if there is a matching policy with the same security context as given, it will be reused. If there is no
// matching policy the returned pod will be nil and the pspName empty. validatedPSPHint is the validated psp name
// saved in kubernetes.io/psp annotation. This psp is usually the one we are looking for.
func (i *IamRoleCache) computeIAMRoleForPod(ctx context.Context, pod *v1.Pod) (*v1alpha1.IamRole, error) {

	if len(pod.Spec.ServiceAccountName) == 0 {
		return nil, nil
	}

	user := "system:serviceaccount:" + pod.ObjectMeta.Namespace + ":" + pod.Spec.ServiceAccountName

	return i.ComputeIAMRole(ctx, user)
}

// PodRole returns the IAM role specified in the annotation for the Pod
func (i *IamRoleCache) PodRole(pod *v1.Pod) *v1alpha1.IamRole {
	if roleArn, ok := pod.ObjectMeta.Annotations[AnnotationIAMRoleKey]; ok {
		return &v1alpha1.IamRole{
			Definition: v1alpha1.IamRoleDefinition{
				RoleArn: roleArn,
			},
		}
	}

	role, err := i.computeIAMRoleForPod(context.TODO(), pod)
	if err != nil {
		return nil
	}
	return role
}

func (c *IamRoleCache) ComputeIAMRole(ctx context.Context, user string) (*v1alpha1.IamRole, error) {

	iamRoles, err := c.lister.List(labels.Everything())

	if err != nil {
		return nil, err
	}

	// return if no IAM roles match
	if len(iamRoles) == 0 {
		return nil, nil
	}

	// order results by alphabetical order
	sort.SliceStable(iamRoles, func(i, j int) bool {

		return strings.Compare(iamRoles[i].Name, iamRoles[j].Name) < 0
	})

	for _, iamRole := range iamRoles {
		if !c.isAuthorizedForRole(ctx, user, iamRole.GetNamespace(), iamRole.Name) {
			continue
		}
		return iamRole, nil
	}

	return nil, nil

}

func (c *IamRoleCache) isAuthorizedForRole(ctx context.Context, sa string, namespace, roleName string) bool {
	if len(sa) == 0 {
		return false
	}
	sar := &authv1.SubjectAccessReview{
		Spec: authv1.SubjectAccessReviewSpec{
			UID: sa,
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      "use",
				Group:     "iam.amazonaws.com",
				Version:   "v1alpha1",
				Resource:  "IamRole",
				Name:      roleName,
			},
		},
	}

	res, err := c.corev1Client.Authorization().SubjectAccessReviews().Create(sar)

	if err != nil {
		log.Errorf("Unexpected error performing subject access review: %+v", err)
		return false
	}

	return res.Status.Allowed
}

func OnAdd(obj interface{}) {
	iamRole, isIamRole := obj.(*v1alpha1.IamRole)
	if !isIamRole {
		log.Errorf("OnAdd unexpected object: %+v", obj)
		return
	}
	log.WithFields(IamRoleFields(iamRole)).Debugf("added role")

}

func OnDelete(obj interface{}) {
	iamRole, isIamRole := obj.(*v1alpha1.IamRole)
	if !isIamRole {
		deletedObj, isDeleted := obj.(cache.DeletedFinalStateUnknown)
		if !isDeleted {
			log.Errorf("OnDelete unexpected object: %+v", obj)
			return
		}

		iamRole, isIamRole = deletedObj.Obj.(*v1alpha1.IamRole)
		if !isIamRole {
			log.Errorf("OnDelete unexpected DeletedFinalStateUnknown object: %+v", deletedObj.Obj)
		}
		log.WithFields(IamRoleFields(iamRole)).Debugf("deleted role")
		return
	}

	log.WithFields(IamRoleFields(iamRole)).Debugf("deleted role")
	return
}

func OnUpdate(old, new interface{}) {
	iamRole, isIamRole := old.(*v1alpha1.IamRole)
	if !isIamRole {
		log.Errorf("OnUpdate unexpected object: %+v", new)
		return
	}

	log.WithFields(IamRoleFields(iamRole)).Debugf("updated role")
}
