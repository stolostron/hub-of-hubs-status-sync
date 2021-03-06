package dbsyncers

import (
	"context"
	"fmt"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func createK8sResource(ctx context.Context, k8sClient client.Client, resource client.Object) error {
	if resource == nil {
		return nil
	}

	// make sure resource version is empty - otherwise cannot create
	resource.SetResourceVersion("")

	if err := k8sClient.Create(ctx, resource); err != nil {
		return fmt.Errorf("failed to create k8s-resource - %w", err)
	}

	return nil
}

func setOwnerReference(resource client.Object, ownerReference *v1.OwnerReference) {
	resource.SetOwnerReferences([]v1.OwnerReference{*ownerReference})
}

func createOwnerReference(apiVersion string, kind string, name string, uid string) *v1.OwnerReference {
	return &v1.OwnerReference{
		APIVersion:         apiVersion,
		Controller:         newTrue(),
		BlockOwnerDeletion: newTrue(),
		Kind:               kind,
		Name:               name,
		UID:                types.UID(uid),
	}
}

func newTrue() *bool {
	t := true
	return &t
}
