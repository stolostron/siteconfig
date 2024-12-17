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
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"golang.org/x/exp/maps"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

type action string

const (
	actionRender   action = "render"
	actionSuppress action = "suppress"
	actionPrune    action = "prune"
)

type RenderedObject struct {
	object unstructured.Unstructured
	action action
}

type SyncWaveMap struct {
	data map[int][]RenderedObject
}

type RenderedObjectCollection struct {
	prune, suppress, render SyncWaveMap
}

func (r *RenderedObject) SetObject(manifest map[string]interface{}) error {
	// Marshal the input manifest to JSON
	content, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, &r.object)
}

func (r *RenderedObject) GetObject() unstructured.Unstructured {
	return r.object
}

func (r *RenderedObject) GetAPIVersion() string {
	return r.object.GetAPIVersion()
}

func (r *RenderedObject) GetKind() string {
	return r.object.GetKind()
}

func (r *RenderedObject) GetGroupVersionKind() schema.GroupVersionKind {
	return r.object.GroupVersionKind()
}

func (r *RenderedObject) GetNamespace() string {
	return r.object.GetNamespace()
}

func (r *RenderedObject) GetName() string {
	return r.object.GetName()
}

func (r *RenderedObject) GetAnnotations() map[string]string {
	return r.object.GetAnnotations()
}

func (r *RenderedObject) GetLabels() map[string]string {
	return r.object.GetLabels()
}

func (r *RenderedObject) GetSyncWave() (int, error) {
	syncWaveStr, ok := r.GetAnnotations()[WaveAnnotation]
	if !ok {
		syncWaveStr = DefaultWaveAnnotation
	}

	return strconv.Atoi(syncWaveStr)
}

func (r *RenderedObject) GetResourceId() string {
	return GetResourceId(r.GetName(), r.GetNamespace(), r.GetKind())
}

func (r *RenderedObject) ManifestReference() *v1alpha1.ManifestReference {
	syncWave, err := r.GetSyncWave()
	if err != nil {
		return nil
	}
	return &v1alpha1.ManifestReference{
		APIGroup:        ptr.To(r.GetAPIVersion()),
		Kind:            r.GetKind(),
		Name:            r.GetName(),
		Namespace:       r.GetNamespace(),
		SyncWave:        syncWave,
		LastAppliedTime: metav1.Now(),
	}
}

// MatchesIdentity checks if two RenderedObjects are equal based on identifying fields.
// These fields are APIGroup, Kind, Name, and Namespace.
func (r *RenderedObject) MatchesIdentity(other *RenderedObject) bool {
	return r.GetAPIVersion() == other.GetAPIVersion() &&
		r.GetKind() == other.GetKind() &&
		r.GetName() == other.GetName() &&
		r.GetNamespace() == other.GetNamespace()
}

// IndexOfObjectByIdentity searches for a RenderedObject in the given RenderedObject slice based on
// identity fields and returns its index.
// It returns -1 and a not found error if the target is not found.
func IndexOfObjectByIdentity(target *RenderedObject, objects []RenderedObject) (int, error) {
	for i, obj := range objects {
		if obj.MatchesIdentity(target) {
			return i, nil
		}
	}
	return -1, fmt.Errorf("RenderedObject (%s) not found", target.GetResourceId())
}

func (s *SyncWaveMap) GetAscendingSyncWaves() []int {
	syncWaves := maps.Keys(s.data)
	sort.Ints(syncWaves)
	return syncWaves
}

func (s *SyncWaveMap) GetDescendingSyncWaves() []int {
	syncWaves := maps.Keys(s.data)
	sort.Sort(sort.Reverse(sort.IntSlice(syncWaves)))
	return syncWaves
}

func (s *SyncWaveMap) GetObjectsForSyncWave(syncWave int) []RenderedObject {
	if res, ok := s.data[syncWave]; ok {
		// sort manifests alphabetically (by "kind") to yield more deterministic results
		sort.Slice(res, func(x, y int) bool {
			return res[x].GetKind() < res[y].GetKind()
		})
		return res
	}
	return []RenderedObject{}
}

func (s *SyncWaveMap) Add(obj RenderedObject) error {
	if obj.GetObject().Object == nil {
		return nil
	}

	syncWave, err := obj.GetSyncWave()
	if err != nil {
		return err
	}

	if s.data == nil {
		s.data = make(map[int][]RenderedObject)
	}

	// check if the key exists in the data
	if _, ok := s.data[syncWave]; !ok {
		// if key doesn't exist, initialize the slice
		s.data[syncWave] = make([]RenderedObject, 0)
	}
	// append the object to the collection associated with the syncWave key
	s.data[syncWave] = append(s.data[syncWave], obj)
	return nil
}

func (s *SyncWaveMap) AddObjects(objects []RenderedObject) error {
	var errs []error
	for _, object := range objects {
		if err := s.Add(object); err != nil {
			errs = append(errs, fmt.Errorf("error adding object (%s) to SyncWaveMap, err: %v",
				object.GetResourceId(), err.Error()))
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (r *RenderedObjectCollection) AddObject(object RenderedObject) error {
	var err error
	switch object.action {
	case actionRender:
		err = r.render.Add(object)
	case actionPrune:
		err = r.prune.Add(object)
	case actionSuppress:
		err = r.suppress.Add(object)
	}

	return err
}

func (r *RenderedObjectCollection) AddObjects(objects []RenderedObject) error {
	var errs []error
	for _, object := range objects {
		if err := r.AddObject(object); err != nil {
			errs = append(errs, fmt.Errorf("error adding object (%s) to RenderedObjectCollection, err: %v",
				object.GetResourceId(), err.Error()))
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (r *RenderedObjectCollection) GetRenderObjects() []RenderedObject {
	renderList := make([]RenderedObject, 0)
	for _, syncWave := range r.render.GetAscendingSyncWaves() {
		renderList = append(renderList, r.render.GetObjectsForSyncWave(syncWave)...)
	}
	return renderList
}

func (r *RenderedObjectCollection) GetPruneObjects() []RenderedObject {
	pruneList := make([]RenderedObject, 0)
	for _, syncWave := range r.prune.GetDescendingSyncWaves() {
		pruneList = append(pruneList, r.prune.GetObjectsForSyncWave(syncWave)...)
	}
	return pruneList
}

func (r *RenderedObjectCollection) GetSuppressObjects() []RenderedObject {
	suppressList := make([]RenderedObject, 0)
	for _, syncWave := range r.suppress.GetAscendingSyncWaves() {
		suppressList = append(suppressList, r.suppress.GetObjectsForSyncWave(syncWave)...)
	}
	return suppressList
}
