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

package conditions

import (
	"reflect"
	"testing"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFindCDConditionType(t *testing.T) {
	condition := hivev1.ClusterDeploymentCondition{
		Type:   hivev1.ClusterInstallCompletedClusterDeploymentCondition,
		Status: corev1.ConditionTrue,
	}
	type args struct {
		conditions []hivev1.ClusterDeploymentCondition
		condType   hivev1.ClusterDeploymentConditionType
	}
	tests := []struct {
		name string
		args args
		want *hivev1.ClusterDeploymentCondition
	}{
		{
			name: "ClusterDeployment condition exists",
			args: args{
				conditions: []hivev1.ClusterDeploymentCondition{
					{
						Type: hivev1.ClusterHibernatingCondition,
					},
					{
						Type: hivev1.ClusterHibernatingCondition,
					},
					condition,
					{
						Type: hivev1.ClusterInstallFailedClusterDeploymentCondition,
					},
				},
				condType: condition.Type,
			},
			want: &condition,
		},
		{
			name: "ClusterDeployment condition does not exist",
			args: args{
				conditions: []hivev1.ClusterDeploymentCondition{
					{
						Type: hivev1.ClusterHibernatingCondition,
					},
					{
						Type: hivev1.ClusterHibernatingCondition,
					},
					{
						Type: hivev1.ClusterInstallFailedClusterDeploymentCondition,
					},
				},
				condType: condition.Type,
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FindCDConditionType(tt.args.conditions, tt.args.condType); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FindCDConditionType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindStatusCondition(t *testing.T) {
	condition := metav1.Condition{
		Type:   string(v1alpha1.ClusterInstanceValidated),
		Status: metav1.ConditionTrue,
	}
	type args struct {
		conditions    []metav1.Condition
		conditionType string
	}
	tests := []struct {
		name string
		args args
		want *metav1.Condition
	}{
		{
			name: "Status condition exists",
			args: args{
				conditions: []metav1.Condition{
					{
						Type: string(v1alpha1.RenderedTemplates),
					},
					{
						Type: string(v1alpha1.RenderedTemplatesValidated),
					},
					condition,
					{
						Type: string(v1alpha1.RenderedTemplatesApplied),
					},
				},
				conditionType: condition.Type,
			},
			want: &condition,
		},
		{
			name: "Status condition does not exist",
			args: args{
				conditions: []metav1.Condition{
					{
						Type: string(v1alpha1.RenderedTemplates),
					},
					{
						Type: string(v1alpha1.RenderedTemplatesValidated),
					},
					{
						Type: string(v1alpha1.RenderedTemplatesApplied),
					},
				},
				conditionType: condition.Type,
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FindStatusCondition(tt.args.conditions, tt.args.conditionType); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FindStatusCondition() = %v, want %v", got, tt.want)
			}
		})
	}
}
