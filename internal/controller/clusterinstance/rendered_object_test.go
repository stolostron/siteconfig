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

package clusterinstance

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestRenderedObjectGetAPIVersion(t *testing.T) {
	tests := []struct {
		name  string
		input RenderedObject
		want  string
	}{
		{
			name: "apiVersion is not specified",
			input: RenderedObject{
				object: unstructured.Unstructured{Object: map[string]interface{}{"foo": "bar"}},
				action: actionRender,
			},
			want: "",
		},
		{
			name: "apiVersion is specified",
			input: RenderedObject{
				object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1"}},
				action: actionRender,
			},
			want: "v1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.GetAPIVersion(); got != tt.want {
				t.Errorf("RenderedObject.GetAPIVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderedObjectGetKind(t *testing.T) {
	tests := []struct {
		name  string
		input RenderedObject
		want  string
	}{
		{
			name: "kind is not specified",
			input: RenderedObject{
				object: unstructured.Unstructured{Object: map[string]interface{}{"foo": "bar"}},
				action: actionRender,
			},
			want: "",
		},
		{
			name: "kind is specified",
			input: RenderedObject{
				object: unstructured.Unstructured{Object: map[string]interface{}{"kind": "ConfigMap"}},
				action: actionRender,
			},
			want: "ConfigMap",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.GetKind(); got != tt.want {
				t.Errorf("RenderedObject.GetKind() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderedObjectGetNamespace(t *testing.T) {
	tests := []struct {
		name  string
		input RenderedObject
		want  string
	}{
		{
			name: "namespace is not specified",
			input: RenderedObject{
				object: unstructured.Unstructured{Object: map[string]interface{}{"foo": "bar"}},
				action: actionRender,
			},
			want: "",
		},
		{
			name: "namespace is specified",
			input: RenderedObject{
				object: unstructured.Unstructured{Object: map[string]interface{}{"metadata": map[string]interface{}{"namespace": "default"}}},
				action: actionRender,
			},
			want: "default",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.GetNamespace(); got != tt.want {
				t.Errorf("RenderedObject.GetNamespace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderedObjectGetName(t *testing.T) {
	tests := []struct {
		name  string
		input RenderedObject
		want  string
	}{
		{
			name: "name is not specified",
			input: RenderedObject{
				object: unstructured.Unstructured{Object: map[string]interface{}{"foo": "bar", "metadata": map[string]interface{}{"notName": "test"}}},
				action: actionRender,
			},
			want: "",
		},
		{
			name: "name is specified",
			input: RenderedObject{
				object: unstructured.Unstructured{Object: map[string]interface{}{"kind": "ConfigMap", "metadata": map[string]interface{}{"name": "test"}}},
				action: actionRender,
			},
			want: "test",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.GetName(); got != tt.want {
				t.Errorf("RenderedObject.GetName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderedObjectGetAnnotations(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]interface{}
		want  map[string]string
	}{
		{
			name:  "metadata is not specified",
			input: map[string]interface{}{"annotations": "bar", "metadata": map[string]interface{}{"noAnnotations": "test"}},
			want:  nil,
		},
		{
			name:  "metadata-annotation is specified",
			input: map[string]interface{}{"metadata": map[string]interface{}{"annotations": map[string]string{"test": "data"}}},
			want:  map[string]string{"test": "data"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			object := RenderedObject{}
			_ = object.SetObject(tt.input)
			if got := object.GetAnnotations(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RenderedObject.GetAnnotations() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderedObjectGetLabels(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]interface{}
		want  map[string]string
	}{
		{
			name:  "metadata is not specified",
			input: map[string]interface{}{"labels": "bar", "metadata": map[string]interface{}{"nolabels": "test"}},
			want:  nil,
		},
		{
			name:  "metadata-labels is specified",
			input: map[string]interface{}{"metadata": map[string]interface{}{"labels": map[string]string{"test": "data"}}},
			want:  map[string]string{"test": "data"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			object := RenderedObject{}
			_ = object.SetObject(tt.input)
			if got := object.GetLabels(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RenderedObject.GetLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderedObjectGetSyncWave(t *testing.T) {
	tests := []struct {
		name    string
		input   map[string]interface{}
		want    int
		wantErr bool
	}{
		{
			name:    "syncWave is not specified",
			input:   map[string]interface{}{"annotations": "bar", "metadata": map[string]interface{}{"noAnnotations": "test"}},
			want:    0,
			wantErr: false,
		},
		{
			name:    "syncWave is specified",
			input:   map[string]interface{}{"metadata": map[string]interface{}{"annotations": map[string]string{WaveAnnotation: "1"}}},
			want:    1,
			wantErr: false,
		},
		{
			name:    "non-integer syncWave is specified",
			input:   map[string]interface{}{"metadata": map[string]interface{}{"annotations": map[string]string{WaveAnnotation: "one"}}},
			want:    0,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			object := RenderedObject{}
			_ = object.SetObject(tt.input)
			got, err := object.GetSyncWave()
			if tt.wantErr && err == nil {
				t.Error("RenderedObject.GetSyncWave(), expected err")
			} else {
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("RenderedObject.GetSyncWave() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestRenderedObjectCollectionGetRenderObjects(t *testing.T) {
	render := SyncWaveMap{
		data: map[int][]RenderedObject{
			0: {
				{
					action: actionRender,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "render/v1"}},
				},
				{
					action: actionRender,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "render/v2"}},
				},
			},
		},
	}
	prune := SyncWaveMap{
		data: map[int][]RenderedObject{
			0: {
				{
					action: actionPrune,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "prune/v1"}},
				},
				{
					action: actionPrune,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "prune/v2"}},
				},
			},
		},
	}
	suppress := SyncWaveMap{
		data: map[int][]RenderedObject{
			0: {
				{
					action: actionSuppress,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "suppress/v1"}},
				},
				{
					action: actionSuppress,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "suppress/v2"}},
				},
			},
		},
	}
	type fields struct {
		prune    SyncWaveMap
		suppress SyncWaveMap
		render   SyncWaveMap
	}
	tests := []struct {
		name   string
		fields fields
		want   []RenderedObject
	}{
		{
			name: "render objects is empty",
			fields: fields{
				prune:    SyncWaveMap{},
				suppress: SyncWaveMap{},
				render:   SyncWaveMap{},
			},
			want: []RenderedObject{},
		},
		{
			name: "render objects is correctly returned",
			fields: fields{
				prune:    prune,
				suppress: suppress,
				render:   render,
			},
			want: render.GetObjectsForSyncWave(0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RenderedObjectCollection{
				prune:    tt.fields.prune,
				suppress: tt.fields.suppress,
				render:   tt.fields.render,
			}
			if got := r.GetRenderObjects(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RenderedObjectCollection.GetRenderObjects() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderedObjectCollectionGetPruneObjects(t *testing.T) {
	render := SyncWaveMap{
		data: map[int][]RenderedObject{
			0: {
				{
					action: actionRender,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "render/v1"}},
				},
				{
					action: actionRender,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "render/v2"}},
				},
			},
		},
	}
	prune := SyncWaveMap{
		data: map[int][]RenderedObject{
			0: {
				{
					action: actionPrune,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "prune/v1"}},
				},
				{
					action: actionPrune,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "prune/v2"}},
				},
			},
		},
	}
	suppress := SyncWaveMap{
		data: map[int][]RenderedObject{
			0: {
				{
					action: actionSuppress,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "suppress/v1"}},
				},
				{
					action: actionSuppress,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "suppress/v2"}},
				},
			},
		},
	}
	type fields struct {
		prune    SyncWaveMap
		suppress SyncWaveMap
		render   SyncWaveMap
	}
	tests := []struct {
		name   string
		fields fields
		want   []RenderedObject
	}{
		{
			name: "prune objects is empty",
			fields: fields{
				prune:    SyncWaveMap{},
				suppress: SyncWaveMap{},
				render:   SyncWaveMap{},
			},
			want: []RenderedObject{},
		},
		{
			name: "prune objects is correctly returned",
			fields: fields{
				prune:    prune,
				suppress: suppress,
				render:   render,
			},
			want: prune.GetObjectsForSyncWave(0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RenderedObjectCollection{
				prune:    tt.fields.prune,
				suppress: tt.fields.suppress,
				render:   tt.fields.render,
			}
			if got := r.GetPruneObjects(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RenderedObjectCollection.GetPruneObjects() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderedObjectCollectionGetSuppressObjects(t *testing.T) {
	render := SyncWaveMap{
		data: map[int][]RenderedObject{
			0: {
				{
					action: actionRender,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "render/v1"}},
				},
				{
					action: actionRender,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "render/v2"}},
				},
			},
		},
	}
	prune := SyncWaveMap{
		data: map[int][]RenderedObject{
			0: {
				{
					action: actionPrune,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "prune/v1"}},
				},
				{
					action: actionPrune,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "prune/v2"}},
				},
			},
		},
	}
	suppress := SyncWaveMap{
		data: map[int][]RenderedObject{
			0: {
				{
					action: actionSuppress,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "suppress/v1"}},
				},
				{
					action: actionSuppress,
					object: unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "suppress/v2"}},
				},
			},
		},
	}
	type fields struct {
		prune    SyncWaveMap
		suppress SyncWaveMap
		render   SyncWaveMap
	}
	tests := []struct {
		name   string
		fields fields
		want   []RenderedObject
	}{
		{
			name: "suppress objects is empty",
			fields: fields{
				prune:    SyncWaveMap{},
				suppress: SyncWaveMap{},
				render:   SyncWaveMap{},
			},
			want: []RenderedObject{},
		},
		{
			name: "suppress objects is correctly returned",
			fields: fields{
				prune:    prune,
				suppress: suppress,
				render:   render,
			},
			want: suppress.GetObjectsForSyncWave(0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RenderedObjectCollection{
				prune:    tt.fields.prune,
				suppress: tt.fields.suppress,
				render:   tt.fields.render,
			}
			if got := r.GetSuppressObjects(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RenderedObjectCollection.GetSuppressObjects() = %v, want %v", got, tt.want)
			}
		})
	}
}
