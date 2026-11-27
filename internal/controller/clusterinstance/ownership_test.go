/*
Copyright 2025.

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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateOwnedByLabelValue(t *testing.T) {
	testcases := []struct {
		name          string
		value         string
		expectedError bool
	}{
		{
			name:  "valid value",
			value: "cluster-namespace_cluster-name",
		},
		{
			name:  "valid value at the 63 character limit",
			value: strings.Repeat("a", 63),
		},
		{
			name:          "value exceeding the 63 character limit",
			value:         strings.Repeat("a", 64),
			expectedError: true,
		},
		{
			name:          "value with an invalid character",
			value:         "cluster-namespace/cluster-name",
			expectedError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOwnedByLabelValue(tc.value)
			if tc.expectedError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
