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

package configuration

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const SiteConfigOperatorConfigMap = "siteconfig-operator-configuration"

const (
	DefaultAllowReinstalls         = false
	DefaultMaxConcurrentReconciles = 1
)

type Configuration struct {
	AllowReinstalls         bool `json:"allowReinstalls"`
	MaxConcurrentReconciles int  `json:"maxConcurrentReconciles"`
}

func NewDefaultConfiguration() *Configuration {
	return &Configuration{
		AllowReinstalls:         DefaultAllowReinstalls,
		MaxConcurrentReconciles: DefaultMaxConcurrentReconciles,
	}
}

func CreateDefaultConfigurationConfigMap(ctx context.Context, c client.Client, namespace string) (*Configuration, error) {

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SiteConfigOperatorConfigMap,
			Namespace: namespace,
		},
		Data: NewDefaultConfiguration().ToMap(),
	}

	if err := c.Create(ctx, cm); err != nil {
		if !errors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create default SiteConfig Configuration: %w", err)
		}
		// Configuration exists, return it instead
		return LoadFromConfigMap(ctx, c, namespace)
	}

	return NewDefaultConfiguration(), nil
}

func LoadFromConfigMap(ctx context.Context, c client.Client, namespace string) (*Configuration, error) {
	// Fetch the ConfigMap
	cm := &corev1.ConfigMap{}
	err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: SiteConfigOperatorConfigMap}, cm)
	if err != nil {
		return nil, err
	}

	return FromMap(cm.Data)
}

func (c *Configuration) ToMap() map[string]string {
	return map[string]string{
		"allowReinstalls":         strconv.FormatBool(c.AllowReinstalls),
		"maxConcurrentReconciles": strconv.Itoa(c.MaxConcurrentReconciles),
	}
}

func FromMap(input map[string]string) (*Configuration, error) {
	config := &Configuration{}
	for key, value := range input {
		switch key {
		case "allowReinstalls":
			boolValue, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("invalid value for %q: %w", key, err)
			}
			config.AllowReinstalls = boolValue

		case "maxConcurrentReconciles":
			intValue, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid value for %q: %w", key, err)
			}
			config.MaxConcurrentReconciles = intValue

		default:
			return nil, fmt.Errorf("unsupported key in input map: %q", key)
		}
	}
	return config, nil
}
