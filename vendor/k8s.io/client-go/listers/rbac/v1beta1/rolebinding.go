/*
Copyright The Kubernetes Authors.

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

// Code generated by lister-gen. DO NOT EDIT.

package v1beta1

import (
	v1beta1 "k8s.io/api/rbac/v1beta1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/listers"
	"k8s.io/client-go/tools/cache"
)

// RoleBindingLister helps list RoleBindings.
// All objects returned here must be treated as read-only.
type RoleBindingLister interface {
	// List lists all RoleBindings in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.RoleBinding, err error)
	// RoleBindings returns an object that can list and get RoleBindings.
	RoleBindings(namespace string) RoleBindingNamespaceLister
	RoleBindingListerExpansion
}

// roleBindingLister implements the RoleBindingLister interface.
type roleBindingLister struct {
	listers.ResourceIndexer[*v1beta1.RoleBinding]
}

// NewRoleBindingLister returns a new RoleBindingLister.
func NewRoleBindingLister(indexer cache.Indexer) RoleBindingLister {
	return &roleBindingLister{listers.New[*v1beta1.RoleBinding](indexer, v1beta1.Resource("rolebinding"))}
}

// RoleBindings returns an object that can list and get RoleBindings.
func (s *roleBindingLister) RoleBindings(namespace string) RoleBindingNamespaceLister {
	return roleBindingNamespaceLister{listers.NewNamespaced[*v1beta1.RoleBinding](s.ResourceIndexer, namespace)}
}

// RoleBindingNamespaceLister helps list and get RoleBindings.
// All objects returned here must be treated as read-only.
type RoleBindingNamespaceLister interface {
	// List lists all RoleBindings in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.RoleBinding, err error)
	// Get retrieves the RoleBinding from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1beta1.RoleBinding, error)
	RoleBindingNamespaceListerExpansion
}

// roleBindingNamespaceLister implements the RoleBindingNamespaceLister
// interface.
type roleBindingNamespaceLister struct {
	listers.ResourceIndexer[*v1beta1.RoleBinding]
}
