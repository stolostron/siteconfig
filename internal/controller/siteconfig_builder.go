/*
Copyright 2024.

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

package controller

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/sakhoury/siteconfig/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

type SiteConfigBuilder struct {
	Log logr.Logger
}

func NewSiteConfigBuilder(pLog logr.Logger) *SiteConfigBuilder {
	scBuilder := SiteConfigBuilder{Log: pLog}

	return &scBuilder
}

// getConfigMap retrieves the configmap from cluster for a given TemplateRef
func getConfigMap(ctx context.Context, c client.Client, templateRef v1alpha1.TemplateRef) (*corev1.ConfigMap, error) {

	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{
		Name:      templateRef.Name,
		Namespace: templateRef.Namespace,
	}, cm); err != nil {
		return nil, err
	}

	return cm, nil
}
