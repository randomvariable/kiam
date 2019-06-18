/*
Copyright 2019 The Kiam authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// NOTE: Boilerplate only.  Ignore this file.

// Package v1alpha1 contains API Schema definitions for the cluster v1alpha1 API group
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen=package,register
// +k8s:conversion-gen=github.com/uswitch/kiam/pkg/apis/iam
// +k8s:defaulter-gen=TypeMeta
// +groupName=iam.amazonaws.com
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/runtime/scheme"
)

const (
	// RoleFinalizer is set on PrepareForCreate callback.
	RoleFinalizer = "role.iam.amazonaws.com"

	// RolePodLabelName is the label set on machines linked to a cluster.
	RolePodLabelName = "iam.amazonaws.com/role-ref"

	// Group is the API group
	Group = "iam.amazonaws.com"

	// Version is the version fo this API
	Version = "v1alpha1"
)

var (
	// SchemeGroupVersion is group version used to register these objects.
	SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: Version}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}

	// AddToScheme adds registered types to the builder.
	// Required by pkg/client/...
	// TODO(pwittrock): Remove this after removing pkg/client/...
	AddToScheme = SchemeBuilder.AddToScheme
)

// Required by pkg/client/listers/...
// TODO(pwittrock): Remove this after removing pkg/client/...
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}
