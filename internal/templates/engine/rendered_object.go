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

package engine

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/stolostron/siteconfig/internal/controller/common"
	"golang.org/x/exp/maps"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

type syncWaveMap struct {
	data map[int][]RenderedObject
}

type RenderedObjectCollection struct {
	prune, suppress, render syncWaveMap
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
	syncWaveStr, ok := r.GetAnnotations()[common.WaveAnnotation]
	if !ok {
		syncWaveStr = common.DefaultWaveAnnotation
	}

	return strconv.Atoi(syncWaveStr)
}

func (r *RenderedObject) GetResourceId() string {
	return common.GetResourceId(r.GetName(), r.GetNamespace(), r.GetKind())
}

func (s *syncWaveMap) getAscendingSyncWaves() []int {
	syncWaves := maps.Keys(s.data)
	sort.Ints(syncWaves)
	return syncWaves
}

func (s *syncWaveMap) getDescendingSyncWaves() []int {
	syncWaves := maps.Keys(s.data)
	sort.Sort(sort.Reverse(sort.IntSlice(syncWaves)))
	return syncWaves
}

func (s *syncWaveMap) getSlice(syncWave int) []RenderedObject {
	if res, ok := s.data[syncWave]; ok {
		// sort manifests alphabetically (by "kind") to yield more deterministic results
		sort.Slice(res, func(x, y int) bool {
			return res[x].GetKind() < res[y].GetKind()
		})
		return res
	}
	return []RenderedObject{}
}

func (s *syncWaveMap) add(obj RenderedObject) error {
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

func (r *RenderedObjectCollection) AddObject(object RenderedObject) error {
	var err error
	switch object.action {
	case actionRender:
		err = r.render.add(object)
	case actionPrune:
		err = r.prune.add(object)
	case actionSuppress:
		err = r.suppress.add(object)
	}

	return err
}

func (r *RenderedObjectCollection) AddObjects(objects []RenderedObject) error {
	for _, object := range objects {
		if err := r.AddObject(object); err != nil {
			return err
		}
	}
	return nil
}

func (r *RenderedObjectCollection) GetRenderObjects() []RenderedObject {
	renderList := make([]RenderedObject, 0)
	for _, syncWave := range r.render.getAscendingSyncWaves() {
		renderList = append(renderList, r.render.getSlice(syncWave)...)
	}
	return renderList
}

func (r *RenderedObjectCollection) GetPruneObjects() []RenderedObject {
	pruneList := make([]RenderedObject, 0)
	for _, syncWave := range r.prune.getDescendingSyncWaves() {
		pruneList = append(pruneList, r.prune.getSlice(syncWave)...)
	}
	return pruneList
}

func (r *RenderedObjectCollection) GetSuppressObjects() []RenderedObject {
	suppressList := make([]RenderedObject, 0)
	for _, syncWave := range r.suppress.getAscendingSyncWaves() {
		suppressList = append(suppressList, r.suppress.getSlice(syncWave)...)
	}
	return suppressList
}
