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

	v1alpha1 "github.com/apecloud/kubeblocks/apis/apps/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeComponentDefinitions implements ComponentDefinitionInterface
type FakeComponentDefinitions struct {
	Fake *FakeAppsV1alpha1
}

var componentdefinitionsResource = v1alpha1.SchemeGroupVersion.WithResource("componentdefinitions")

var componentdefinitionsKind = v1alpha1.SchemeGroupVersion.WithKind("ComponentDefinition")

// Get takes name of the componentDefinition, and returns the corresponding componentDefinition object, and an error if there is any.
func (c *FakeComponentDefinitions) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.ComponentDefinition, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(componentdefinitionsResource, name), &v1alpha1.ComponentDefinition{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.ComponentDefinition), err
}

// List takes label and field selectors, and returns the list of ComponentDefinitions that match those selectors.
func (c *FakeComponentDefinitions) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.ComponentDefinitionList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(componentdefinitionsResource, componentdefinitionsKind, opts), &v1alpha1.ComponentDefinitionList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.ComponentDefinitionList{ListMeta: obj.(*v1alpha1.ComponentDefinitionList).ListMeta}
	for _, item := range obj.(*v1alpha1.ComponentDefinitionList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested componentDefinitions.
func (c *FakeComponentDefinitions) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(componentdefinitionsResource, opts))
}

// Create takes the representation of a componentDefinition and creates it.  Returns the server's representation of the componentDefinition, and an error, if there is any.
func (c *FakeComponentDefinitions) Create(ctx context.Context, componentDefinition *v1alpha1.ComponentDefinition, opts v1.CreateOptions) (result *v1alpha1.ComponentDefinition, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateAction(componentdefinitionsResource, componentDefinition), &v1alpha1.ComponentDefinition{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.ComponentDefinition), err
}

// Update takes the representation of a componentDefinition and updates it. Returns the server's representation of the componentDefinition, and an error, if there is any.
func (c *FakeComponentDefinitions) Update(ctx context.Context, componentDefinition *v1alpha1.ComponentDefinition, opts v1.UpdateOptions) (result *v1alpha1.ComponentDefinition, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateAction(componentdefinitionsResource, componentDefinition), &v1alpha1.ComponentDefinition{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.ComponentDefinition), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeComponentDefinitions) UpdateStatus(ctx context.Context, componentDefinition *v1alpha1.ComponentDefinition, opts v1.UpdateOptions) (*v1alpha1.ComponentDefinition, error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateSubresourceAction(componentdefinitionsResource, "status", componentDefinition), &v1alpha1.ComponentDefinition{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.ComponentDefinition), err
}

// Delete takes name of the componentDefinition and deletes it. Returns an error if one occurs.
func (c *FakeComponentDefinitions) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteActionWithOptions(componentdefinitionsResource, name, opts), &v1alpha1.ComponentDefinition{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeComponentDefinitions) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionAction(componentdefinitionsResource, listOpts)

	_, err := c.Fake.Invokes(action, &v1alpha1.ComponentDefinitionList{})
	return err
}

// Patch applies the patch and returns the patched componentDefinition.
func (c *FakeComponentDefinitions) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.ComponentDefinition, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceAction(componentdefinitionsResource, name, pt, data, subresources...), &v1alpha1.ComponentDefinition{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.ComponentDefinition), err
}
