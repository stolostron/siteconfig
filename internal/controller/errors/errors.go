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

package errors

import (
	"errors"
	"fmt"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

type ClusterInstanceError struct {
	Type    string
	Message string
}

var _ error = &ClusterInstanceError{}

// Error implements the Error interface.
func (e *ClusterInstanceError) Error() string {
	return e.Message
}

// Predefined error types for ClusterInstance operations.
const (
	ErrorTypeNotOwnedObject  = "NOT_OWNED_OBJECT"
	ErrorTypeDeletionTimeout = "DELETION_TIMEOUT"
	ErrorTypeUnknown         = ""
)

// New creates a new ClusterInstanceError with a given code and message.
func New(errType, message string) error {
	return &ClusterInstanceError{
		Type:    errType,
		Message: message,
	}
}

// Helper functions to create specific errors.

// NewNotOwnedObjectError returns an error indicating that the ClusterInstance is not the owner of the object.
func NewNotOwnedObjectError(owner, resourceId string) error {
	return New(ErrorTypeNotOwnedObject,
		fmt.Sprintf("ClusterInstance '%s' is not the owner of object (%s)", owner, resourceId))
}

// NewDeletionTimeoutError returns an error indicating the manifest could not be deleted due to
// a timeout.
func NewDeletionTimeoutError(resourceId string) error {
	return New(ErrorTypeDeletionTimeout,
		fmt.Sprintf("Timed out waiting to delete object (%s)", resourceId))
}

func isErrorOfType(err error, errType string) bool {
	if err == nil {
		return false
	}

	// Check if the error is an aggregate
	var agg utilerrors.Aggregate
	if errors.As(err, &agg) {
		for _, err1 := range agg.Errors() {
			// Unwrap and check if it matches the error type errType
			var ciErr *ClusterInstanceError
			if errors.As(err1, &ciErr) &&
				ciErr.Type == errType {
				return true
			}
		}
	}

	// If not an aggregate, check the single error directly
	var ciErr *ClusterInstanceError
	if errors.As(err, &ciErr) &&
		ciErr.Type == errType {
		return true
	}

	return false
}

// IsNotOwnedObject checks if an error contains a NotOwnedObjectError.
func IsNotOwnedObject(err error) bool {
	return isErrorOfType(err, ErrorTypeNotOwnedObject)
}

// IsDeletionTimeoutError checks if an error contains a DeletionTimeoutError.
func IsDeletionTimeoutError(err error) bool {
	return isErrorOfType(err, ErrorTypeDeletionTimeout)
}
