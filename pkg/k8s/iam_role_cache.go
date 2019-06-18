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

	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"github.com/uswitch/kiam/pkg/apis/iam/v1alpha1"
	"k8s.io/client-go/tools/cache"
)

// IamRoleCache implements a cache, allowing lookups by their IP address
type IamRoleCache struct {
	iamRoles       chan *v1alpha1.IamRole
	indexer    cache.Indexer
	controller cache.Controller
}

// NewPodCache creates the cache object that uses a watcher to listen for Pod events. The cache indexes pods by their
// IP address so that Kiam can identify which role a Pod should assume. It periodically syncs the list of
// pods and can announce Pods. When announcing Pods via the channel it will drop events if the buffer
// is full- bufferSize determines how many.
func NewIamRoleCache(source cache.ListerWatcher, syncInterval time.Duration, bufferSize int) *IamRoleCache {
	indexers := cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}
	iamRoles := make(chan *v1alpha1.IamRole, bufferSize)
	iamRoleHandler := &podHandler{pods}
	indexer, controller := cache.NewIndexerInformer(source, &v1alpha1.IamRole{}, syncInterval, iamRoleHandler, indexers)
	iamRoleCache := &IamRoleCache{
		iamRoles:   iamRoles,
		indexer:    indexer,
		controller: controller,
	}

	return iamRoleCache
}

// FindIamRole finds the IAM role by it's namespace and name
func (c *IamRoleCache) FindIamRole(ctx context.Context, namespace string, name string) (*v1alpha1.IamRole, error) {
	obj, exists, err := c.indexer.GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	return obj.(*v1alpha1.IamRole), nil
}

// IamRoles can be used to watch roles as they're added to the cache, part
// of the RoleAnnouncer interface
func (s *IamRoleCache) IamRoles() <-chan *v1alpha1.IamRole {
	return s.iamRoles
}

var (
	// ErrPodNotFound is returned when there's no matching Pod in the cache.
	ErrRoleNotFound = fmt.Errorf("role not found")
)

// Run starts the controller processing updates. Blocks until the cache has synced
func (s *IamRoleCache) Run(ctx context.Context) error {
	go s.controller.Run(ctx.Done())
	log.Infof("started cache controller")

	ok := cache.WaitForCacheSync(ctx.Done(), s.controller.HasSynced)
	if !ok {
		return ErrWaitingForSync
	}

	return nil
}

type iamRoleHandler struct {
	iamRoles chan<- *v1alpha1.IamRole
}

func (o *iamRoleHandler) announce(iamRole *v1alpha1.IamRole) {
	return
}

func (o *iamRoleHandler) OnAdd(obj interface{}) {
	iamRole, isIamRole := obj.(*v1alpha1.IamRole)
	if !isIamRole {
		log.Errorf("OnAdd unexpected object: %+v", obj)
		return
	}
	log.WithFields(IamRoleFields(iamRole)).Debugf("added role")

	o.announce(iamRole)
}

func (o *iamRoleHandler) OnDelete(obj interface{}) {
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

func (o *iamRoleHandler) OnUpdate(old, new interface{}) {
	iamRole, isIamRole := obj.(*v1alpha1.IamRole)
	if !isIamRole {
		log.Errorf("OnUpdate unexpected object: %+v", new)
		return
	}

	log.WithFields(IamRoleFields(iamRole)).Debugf("updated role")
}
