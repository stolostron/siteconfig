package conditions

import (
	"reflect"
	"testing"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
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
		Type:   string(ClusterInstanceValidated),
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
						Type: string(RenderedTemplates),
					},
					{
						Type: string(RenderedTemplatesValidated),
					},
					condition,
					{
						Type: string(RenderedTemplatesApplied),
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
						Type: string(RenderedTemplates),
					},
					{
						Type: string(RenderedTemplatesValidated),
					},
					{
						Type: string(RenderedTemplatesApplied),
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
