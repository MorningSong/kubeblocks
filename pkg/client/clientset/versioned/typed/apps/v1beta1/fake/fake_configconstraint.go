/*
Copyright (C) 2022-2024 ApeCloud Co., Ltd

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

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	"context"

	v1beta1 "github.com/apecloud/kubeblocks/apis/apps/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeConfigConstraints implements ConfigConstraintInterface
type FakeConfigConstraints struct {
	Fake *FakeAppsV1beta1
}

var configconstraintsResource = v1beta1.SchemeGroupVersion.WithResource("configconstraints")

var configconstraintsKind = v1beta1.SchemeGroupVersion.WithKind("ConfigConstraint")

// Get takes name of the configConstraint, and returns the corresponding configConstraint object, and an error if there is any.
func (c *FakeConfigConstraints) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1beta1.ConfigConstraint, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(configconstraintsResource, name), &v1beta1.ConfigConstraint{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.ConfigConstraint), err
}

// List takes label and field selectors, and returns the list of ConfigConstraints that match those selectors.
func (c *FakeConfigConstraints) List(ctx context.Context, opts v1.ListOptions) (result *v1beta1.ConfigConstraintList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(configconstraintsResource, configconstraintsKind, opts), &v1beta1.ConfigConstraintList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1beta1.ConfigConstraintList{ListMeta: obj.(*v1beta1.ConfigConstraintList).ListMeta}
	for _, item := range obj.(*v1beta1.ConfigConstraintList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested configConstraints.
func (c *FakeConfigConstraints) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(configconstraintsResource, opts))
}

// Create takes the representation of a configConstraint and creates it.  Returns the server's representation of the configConstraint, and an error, if there is any.
func (c *FakeConfigConstraints) Create(ctx context.Context, configConstraint *v1beta1.ConfigConstraint, opts v1.CreateOptions) (result *v1beta1.ConfigConstraint, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateAction(configconstraintsResource, configConstraint), &v1beta1.ConfigConstraint{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.ConfigConstraint), err
}

// Update takes the representation of a configConstraint and updates it. Returns the server's representation of the configConstraint, and an error, if there is any.
func (c *FakeConfigConstraints) Update(ctx context.Context, configConstraint *v1beta1.ConfigConstraint, opts v1.UpdateOptions) (result *v1beta1.ConfigConstraint, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateAction(configconstraintsResource, configConstraint), &v1beta1.ConfigConstraint{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.ConfigConstraint), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeConfigConstraints) UpdateStatus(ctx context.Context, configConstraint *v1beta1.ConfigConstraint, opts v1.UpdateOptions) (*v1beta1.ConfigConstraint, error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateSubresourceAction(configconstraintsResource, "status", configConstraint), &v1beta1.ConfigConstraint{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.ConfigConstraint), err
}

// Delete takes name of the configConstraint and deletes it. Returns an error if one occurs.
func (c *FakeConfigConstraints) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteActionWithOptions(configconstraintsResource, name, opts), &v1beta1.ConfigConstraint{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeConfigConstraints) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionAction(configconstraintsResource, listOpts)

	_, err := c.Fake.Invokes(action, &v1beta1.ConfigConstraintList{})
	return err
}

// Patch applies the patch and returns the patched configConstraint.
func (c *FakeConfigConstraints) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1beta1.ConfigConstraint, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceAction(configconstraintsResource, name, pt, data, subresources...), &v1beta1.ConfigConstraint{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.ConfigConstraint), err
}
