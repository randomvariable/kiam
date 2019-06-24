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
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/uswitch/kiam/pkg/apis/iam/v1alpha1"
	iamV1alpha1 "github.com/uswitch/kiam/pkg/k8s/client/clientset_generated/clientset"
	iamLister "github.com/uswitch/kiam/pkg/k8s/client/listers_generated/iam/v1alpha1"
	iamInformer "github.com/uswitch/kiam/pkg/k8s/client/informers_generated/externalversions"
	"github.com/uswitch/kiam/pkg/k8s/client"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/serviceaccount"
)

// PodCache implements a cache, allowing lookups by their IP address
type PodCache struct {
	pods           chan *v1.Pod
	indexer        cache.Indexer
	controller 	   cache.Controller
	kubeConfig     string
}

// NewPodCache creates the cache object that uses a watcher to listen for Pod events. The cache indexes pods by their
// IP address so that Kiam can identify which role a Pod should assume. It periodically syncs the list of
// pods and can announce Pods. When announcing Pods via the channel it will drop events if the buffer
// is full- bufferSize determines how many.
func NewPodCache(source cache.ListerWatcher, kubeConfig string, syncInterval time.Duration, bufferSize int) *PodCache {
	indexers := cache.Indexers{
		indexPodIP:   podIPIndex,
		indexPodRole: podRoleIndex,
	}
	pods := make(chan *v1.Pod, bufferSize)
	podHandler := &podHandler{pods}
	indexer, controller := cache.NewIndexerInformer(source, &v1.Pod{}, syncInterval, podHandler, indexers)

	podCache := &PodCache{
		pods:       pods,
		indexer:    indexer,
		controller: controller,
		kubeConfig: kubeConfig,
	}

	return podCache
}

// ErrMultipleRunningPods indicates that multiple pods were found. This is
// an error as we expect IP addresses to not overlap
var ErrMultipleRunningPods = fmt.Errorf("multiple running pods found")

// IsPodCompleted returns true for Pods that are Pending or Running.
func IsPodCompleted(pod *v1.Pod) bool {
	return pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed
}

// Pods can be used to watch pods as they're added to the cache, part
// of the PodAnnouncer interface
func (s *PodCache) Pods() <-chan *v1.Pod {
	return s.pods
}

// IsActivePodsForRole returns whether there are any uncompleted pods
// using the provided role. This is used to identify whether the
// role credentials should be maintained. Part of the PodAnnouncer
// interface
func (s *PodCache) IsActivePodsForRole(role string) (bool, error) {
	items, err := s.indexer.ByIndex(indexPodRole, role)
	if err != nil {
		return false, err
	}

	for _, obj := range items {
		pod, _ := obj.(*v1.Pod)

		if !IsPodCompleted(pod) {
			return true, nil
		}
	}

	return false, nil
}

var (
	// ErrPodNotFound is returned when there's no matching Pod in the cache.
	ErrPodNotFound = fmt.Errorf("pod not found")
	// ErrWaitingForSync indicates there was an error while waiting for the cache
	// to perform a sync with the api server.
	ErrWaitingForSync = fmt.Errorf("error waiting for cache sync")
)

// findPodForIP returns the Pod identified by the provided IP address. The
// Pod must be active (i.e. pending or running)
func (s *PodCache) findPodForIP(ip string) (*v1.Pod, error) {
	found := make([]*v1.Pod, 0)

	items, err := s.indexer.ByIndex(indexPodIP, ip)
	if err != nil {
		return nil, err
	}

	for _, obj := range items {
		pod := obj.(*v1.Pod)

		if IsPodCompleted(pod) {
			continue
		}

		if pod.Status.PodIP == ip {
			found = append(found, pod)
		}
	}

	for idx, pod := range found {
		log.WithFields(PodFields(pod)).Debugf("found %d/%d pods for ip %s", len(found), idx+1, ip)
	}

	if len(found) == 0 {
		return nil, ErrPodNotFound
	}

	if len(found) == 1 {
		return found[0], nil
	}

	return nil, ErrMultipleRunningPods
}

// GetPodByIP returns the Pod with the provided IP address
func (s *PodCache) GetPodByIP(ip string) (*v1.Pod, error) {
	return s.findPodForIP(ip)
}

const (
	indexPodIP   = "byIP"
	indexPodRole = "byRole"
)

func podIPIndex(obj interface{}) ([]string, error) {
	pod := obj.(*v1.Pod)

	if pod.Status.PodIP == "" {
		return []string{}, nil
	}

	return []string{pod.Status.PodIP}, nil
}

func podRoleIndex(obj interface{}) ([]string, error) {
	pod := obj.(*v1.Pod)
	role := PodRole(pod)
	if role == "" {
		return []string{}, nil
	}

	return []string{role}, nil
}

// Run starts the controller processing updates. Blocks until the cache has synced
func (s *PodCache) Run(ctx context.Context) error {
	go s.controller.Run(ctx.Done())
	log.Infof("started cache controller")

	ok := cache.WaitForCacheSync(ctx.Done(), s.controller.HasSynced)
	if !ok {
		return ErrWaitingForSync
	}

	return nil
}


// computeSecurityContext derives a valid security context while trying to avoid any changes to the given pod. I.e.
// if there is a matching policy with the same security context as given, it will be reused. If there is no
// matching policy the returned pod will be nil and the pspName empty. validatedPSPHint is the validated psp name
// saved in kubernetes.io/psp annotation. This psp is usually the one we are looking for.
func (s *PodCache) computeIAMRole(ctx context.Context, pod *v1.Pod) (*v1alpha1.IamRole, error) {
	// get all constraints that are usable by the user
	log.Infof("getting AWS IAM roles for pod %s (generate: %s)", pod.Name, pod.GenerateName)
	var saInfo user.Info
	if len(pod.Spec.ServiceAccountName) > 0 {
		saInfo = serviceaccount.UserInfo(pod.Namespace, pod.Spec.ServiceAccountName, "")
	}

	iamClientSet, _ := client.NewClient(s.kubeConfig)

	iamRoleLister := iamInformer.NewSharedInformerFactory(iamClientSet, time.Minute)

	iamRoles, err := iamRoleLister.Iam().V1alpha1().IamRoles().Lister().List(labels.Everything())

	if err != nil {
		return nil, err
	}

	// if we have no policies and want to succeed then return.  Otherwise we'll end up with no
	// providers and fail with "unable to validate against any pod security policy" below.
	if len(iamRoles) == 0 && !c.failOnNoPolicies {
		return nil, nil
	}

	// sort policies by name to make order deterministic
	// If mutation is not allowed and validatedPSPHint is provided, check the validated policy first.
	// TODO(liggitt): add priority field to allow admins to bucket differently
	sort.SliceStable(iamRoles, func(i, j int) bool {

		return strings.Compare(iamRoles[i].Name, iamRoles[j].Name) < 0
	})

	for _, iamRole := range iamRoles {
		if !isAuthorizedForPolicy(a.GetUserInfo(), saInfo, a.GetNamespace(), provider.GetPSPName(), c.authz) {
			continue
		}
		return iamRole, nil
	}

	return nil, nil
}



// PodRole returns the IAM role specified in the annotation for the Pod
func PodRole(pod *v1.Pod) string {
	return pod.ObjectMeta.Annotations[AnnotationIAMRoleKey]
}

// AnnotationIAMRoleKey is the key for the annotation specifying the IAM Role
const AnnotationIAMRoleKey = "iam.amazonaws.com/role"

type podHandler struct {
	pods chan<- *v1.Pod
}

func (o *podHandler) announce(pod *v1.Pod) {
	logger := log.WithFields(PodFields(pod))
	if IsPodCompleted(pod) {
		return
	}
	if PodRole(pod) == "" {
		return
	}

	select {
	case o.pods <- pod:
		logger.Debugf("announced pod")
	default:
		dropAnnounce.Inc()
		logger.Warnf("pods announcement full, dropping")
	}
}

func (o *podHandler) OnAdd(obj interface{}) {
	pod, isPod := obj.(*v1.Pod)
	if !isPod {
		log.Errorf("OnAdd unexpected object: %+v", obj)
		return
	}ever
	log.WithFields(PodFields(pod)).Debugf("added pod")

	o.announce(pod)
}

func (o *podHandler) OnDelete(obj interface{}) {
	pod, isPod := obj.(*v1.Pod)
	if !isPod {
		deletedObj, isDeleted := obj.(cache.DeletedFinalStateUnknown)
		if !isDeleted {
			log.Errorf("OnDelete unexpected object: %+v", obj)
			return
		}

		pod, isPod = deletedObj.Obj.(*v1.Pod)
		if !isPod {
			log.Errorf("OnDelete unexpected DeletedFinalStateUnknown object: %+v", deletedObj.Obj)
		}
		log.WithFields(PodFields(pod)).Debugf("deleted pod")
		return
	}

	log.WithFields(PodFields(pod)).Debugf("deleted pod")
	return
}

func (o *podHandler) OnUpdate(old, new interface{}) {
	pod, isPod := new.(*v1.Pod)
	if !isPod {
		log.Errorf("OnUpdate unexpected object: %+v", new)
		return
	}

	log.WithFields(PodFields(pod)).Debugf("updated pod")
}
