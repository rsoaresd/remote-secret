//
// Copyright (c) 2021 Red Hat, Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bindings

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	api "github.com/redhat-appstudio/remote-secret/api/v1beta1"
	"github.com/redhat-appstudio/remote-secret/pkg/commaseparated"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestServiceAccountSecretComparator(t *testing.T) {
	cases := []struct {
		name  string
		equal bool
		a     *corev1.Secret
		b     *corev1.Secret
	}{
		{
			name:  "empty_secrets",
			equal: true,
			a:     &corev1.Secret{},
			b:     &corev1.Secret{},
		},
		{
			name:  "autogenerated_data_fields_ignored",
			equal: true,
			a:     &corev1.Secret{},
			b: &corev1.Secret{
				Data: map[string][]byte{
					"ca.crt":    []byte("cert"),
					"namespace": []byte("ns"),
					"token":     []byte("token"),
				},
			},
		},
		{
			name:  "immutable_not_ignored",
			equal: false,
			a:     &corev1.Secret{},
			b: &corev1.Secret{
				Immutable: ptr.To(false),
			},
		},
		{
			name:  "other_data_fields_not_ignored",
			equal: false,
			a:     &corev1.Secret{},
			b: &corev1.Secret{
				Data: map[string][]byte{
					"extra": []byte("value"),
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := cmp.Equal(*c.a, *c.b, serviceAccountSecretDiffOpts)

			if c.equal {
				assert.True(t, res, cmp.Diff(*c.a, *c.b, serviceAccountSecretDiffOpts))
			} else {
				assert.False(t, res, cmp.Diff(*c.a, *c.b, serviceAccountSecretDiffOpts))
			}
		})
	}
}

func TestServiceAccountSync(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))

	clBld := func() *fake.ClientBuilder {
		return fake.NewClientBuilder().WithScheme(scheme)
	}

	deploymentTarget := &TestDeploymentTarget{}
	objectMarker := &TestObjectMarker{}
	h := serviceAccountHandler{
		Target:       deploymentTarget,
		ObjectMarker: objectMarker,
	}

	t.Run("with SA", func(t *testing.T) {
		cl := clBld().WithObjects(
			&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sa",
					Namespace: "default",
					Labels: map[string]string{
						"a": "b",
						"c": "d",
					},
					Annotations: map[string]string{
						"a": "b",
						"c": "d",
					},
				},
			},
		).Build()

		deploymentTarget.GetClientImpl = func() client.Client { return cl }
		objectMarker.MarkReferencedImpl = func(ctx context.Context, _ client.ObjectKey, o client.Object) (bool, error) {
			o.GetAnnotations()["gelinkt"] = "yay"
			return true, nil
		}
		deploymentTarget.GetSpecImpl = func() api.LinkableSecretSpec {
			return api.LinkableSecretSpec{
				LinkedTo: []api.SecretLink{
					{
						ServiceAccount: api.ServiceAccountLink{
							Reference: corev1.LocalObjectReference{
								Name: "sa",
							},
						},
					},
				},
			}
		}
		deploymentTarget.GetTargetNamespaceImpl = func() string {
			return "default"
		}

		sas, _, err := h.Sync(context.TODO())
		assert.NoError(t, err)

		assert.Len(t, sas, 1)
		sa := sas[0]
		assert.Len(t, sa.Labels, 2)
		assert.Equal(t, sa.Labels["a"], "b")
		assert.Equal(t, sa.Labels["c"], "d")

		assert.Len(t, sa.Annotations, 3)
		assert.Equal(t, sa.Annotations["a"], "b")
		assert.Equal(t, sa.Annotations["c"], "d")
		assert.Equal(t, sa.Annotations["gelinkt"], "yay")

		storedSA := &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKeyFromObject(sa), storedSA))
		assert.Equal(t, sa.Name, storedSA.Name)
		assert.Equal(t, sa.Labels, storedSA.Labels)
		assert.Equal(t, sa.Annotations, storedSA.Annotations)
	})

	t.Run("no SA", func(t *testing.T) {
		cl := clBld().Build()
		deploymentTarget.GetClientImpl = func() client.Client { return cl }
		deploymentTarget.GetSpecImpl = func() api.LinkableSecretSpec {
			return api.LinkableSecretSpec{
				LinkedTo: []api.SecretLink{
					{
						ServiceAccount: api.ServiceAccountLink{
							Managed: api.ManagedServiceAccountSpec{
								GenerateName: "sa",
								Labels: map[string]string{
									"a": "b",
									"c": "d",
								},
								Annotations: map[string]string{
									"a": "b",
									"c": "d",
								},
							},
						},
					},
				},
			}
		}
		objectMarker.MarkManagedImpl = func(ctx context.Context, _ client.ObjectKey, o client.Object) (bool, error) {
			if o.GetLabels() == nil {
				o.SetLabels(map[string]string{})
			}
			o.GetLabels()["gelinkt_managed"] = "yay"
			return true, nil
		}
		deploymentTarget.GetTargetNamespaceImpl = func() string {
			return "default"
		}

		sas, _, err := h.Sync(context.TODO())
		assert.NoError(t, err)

		assert.Len(t, sas, 1)
		sa := sas[0]
		assert.True(t, strings.HasPrefix(sa.Name, "sa"))
		assert.Len(t, sa.Labels, 3)
		assert.Equal(t, "b", sa.Labels["a"])
		assert.Equal(t, "d", sa.Labels["c"])
		assert.Equal(t, "yay", sa.Labels["gelinkt_managed"])

		assert.Len(t, sa.Annotations, 2)
		assert.Equal(t, "b", sa.Annotations["a"])
		assert.Equal(t, "d", sa.Annotations["c"])

		storedSA := &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKeyFromObject(sa), storedSA))
		assert.Equal(t, sa.Name, storedSA.Name)
		assert.Equal(t, sa.Labels, storedSA.Labels)
		assert.Equal(t, sa.Annotations, storedSA.Annotations)
	})

	t.Run("flip referenced to managed", func(t *testing.T) {
		cl := clBld().
			WithObjects(
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sa",
						Namespace: "default",
					},
				},
			).
			Build()

		deploymentTarget.GetClientImpl = func() client.Client { return cl }

		objectMarker.MarkReferencedImpl = func(ctx context.Context, _ client.ObjectKey, o client.Object) (bool, error) {
			if o.GetLabels() == nil {
				o.SetLabels(map[string]string{})
			}
			o.GetLabels()["gelinkt"] = "yay"
			return true, nil
		}
		deploymentTarget.GetTargetNamespaceImpl = func() string {
			return "default"
		}
		deploymentTarget.GetSpecImpl = func() api.LinkableSecretSpec {
			return api.LinkableSecretSpec{
				LinkedTo: []api.SecretLink{
					{
						ServiceAccount: api.ServiceAccountLink{
							Reference: corev1.LocalObjectReference{
								Name: "sa",
							},
						},
					},
				},
			}
		}

		_, _, err := h.Sync(context.TODO())
		assert.NoError(t, err)

		storedSA := &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKey{Name: "sa", Namespace: "default"}, storedSA))
		assert.Equal(t, "sa", storedSA.Name)
		assert.Len(t, storedSA.Labels, 1)
		assert.Equal(t, "yay", storedSA.Labels["gelinkt"])

		deploymentTarget.GetSpecImpl = func() api.LinkableSecretSpec {
			return api.LinkableSecretSpec{
				LinkedTo: []api.SecretLink{
					{
						ServiceAccount: api.ServiceAccountLink{
							Managed: api.ManagedServiceAccountSpec{
								Name: "sa",
							},
						},
					},
				},
			}
		}
		objectMarker.MarkManagedImpl = func(ctx context.Context, _ client.ObjectKey, o client.Object) (bool, error) {
			if o.GetLabels() == nil {
				o.SetLabels(map[string]string{})
			}
			o.GetLabels()["gelinkt_managed"] = "yay"
			return true, nil
		}
		objectMarker.IsReferencedByImpl = func(ctx context.Context, _ client.ObjectKey, o client.Object) (bool, error) {
			return o.GetLabels()["gelinkt"] == "yay", nil
		}

		_, _, err = h.Sync(context.TODO())
		assert.NoError(t, err)

		storedSA = &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKey{Name: "sa", Namespace: "default"}, storedSA))
		assert.Equal(t, "sa", storedSA.Name)
		assert.Equal(t, "yay", storedSA.Labels["gelinkt_managed"])
		assert.Equal(t, "yay", storedSA.Labels["gelinkt"])
	})

	t.Run("flip managed to referenced", func(t *testing.T) {
		cl := clBld().Build()

		deploymentTarget.GetClientImpl = func() client.Client { return cl }

		deploymentTarget.GetSpecImpl = func() api.LinkableSecretSpec {
			return api.LinkableSecretSpec{
				LinkedTo: []api.SecretLink{
					{
						ServiceAccount: api.ServiceAccountLink{
							Managed: api.ManagedServiceAccountSpec{
								Name: "sa",
							},
						},
					},
				},
			}
		}
		objectMarker.MarkManagedImpl = func(ctx context.Context, _ client.ObjectKey, o client.Object) (bool, error) {
			if o.GetLabels() == nil {
				o.SetLabels(map[string]string{})
			}
			o.GetLabels()["gelinkt_managed"] = "yay"
			return true, nil
		}
		deploymentTarget.GetTargetNamespaceImpl = func() string {
			return "default"
		}

		_, _, err := h.Sync(context.TODO())
		assert.NoError(t, err)

		storedSA := &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKey{Name: "sa", Namespace: "default"}, storedSA))
		assert.Equal(t, "sa", storedSA.Name)
		assert.Equal(t, "yay", storedSA.Labels["gelinkt_managed"])

		deploymentTarget.GetSpecImpl = func() api.LinkableSecretSpec {
			return api.LinkableSecretSpec{
				LinkedTo: []api.SecretLink{
					{
						ServiceAccount: api.ServiceAccountLink{
							Reference: corev1.LocalObjectReference{
								Name: "sa",
							},
						},
					},
				},
			}
		}
		objectMarker.UnmarkManagedImpl = func(ctx context.Context, _ client.ObjectKey, o client.Object) (bool, error) {
			delete(o.GetLabels(), "gelinkt_managed")
			return true, nil
		}
		objectMarker.MarkReferencedImpl = func(ctx context.Context, _ client.ObjectKey, o client.Object) (bool, error) {
			if o.GetLabels() == nil {
				o.SetLabels(map[string]string{})
			}
			o.GetLabels()["gelinkt"] = "yay"
			return true, nil
		}

		_, _, err = h.Sync(context.TODO())
		assert.NoError(t, err)

		storedSA = &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKey{Name: "sa", Namespace: "default"}, storedSA))
		assert.Equal(t, "sa", storedSA.Name)
		assert.NotContains(t, "gelinkt_managed", storedSA.Labels)
		assert.Equal(t, "yay", storedSA.Labels["gelinkt"])
	})

	t.Run("disallow taking ownership", func(t *testing.T) {
		cl := clBld().Build()

		deploymentTarget.GetClientImpl = func() client.Client { return cl }

		deploymentTarget.GetSpecImpl = func() api.LinkableSecretSpec {
			return api.LinkableSecretSpec{
				LinkedTo: []api.SecretLink{
					{
						ServiceAccount: api.ServiceAccountLink{
							Managed: api.ManagedServiceAccountSpec{
								Name: "sa",
							},
						},
					},
				},
			}
		}
		objectMarker.MarkManagedImpl = func(ctx context.Context, _ client.ObjectKey, o client.Object) (bool, error) {
			if o.GetLabels() == nil {
				o.SetLabels(map[string]string{})
			}
			o.GetLabels()["gelinkt_managed"] = "yay"
			return true, nil
		}
		deploymentTarget.GetTargetNamespaceImpl = func() string {
			return "default"
		}

		_, _, err := h.Sync(context.TODO())
		assert.NoError(t, err)

		storedSA := &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKey{Name: "sa", Namespace: "default"}, storedSA))
		assert.Equal(t, "sa", storedSA.Name)
		assert.Equal(t, "yay", storedSA.Labels["gelinkt_managed"])

		// now, change the behavior to say that our SA is managed by some other target.
		objectMarker.IsManagedByOtherImpl = func(ctx context.Context, o client.Object) (bool, error) {
			return true, nil
		}
		_, _, err = h.Sync(context.TODO())

		assert.Error(t, err)

		storedSA = &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKey{Name: "sa", Namespace: "default"}, storedSA))
		assert.Equal(t, "sa", storedSA.Name)

		assert.Equal(t, "yay", storedSA.Labels["gelinkt_managed"])
	})

	t.Run("single owner, multiple referenced", func(t *testing.T) {
		cl := clBld().Build()

		deploymentTarget.GetClientImpl = func() client.Client { return cl }

		deploymentTarget.GetSpecImpl = func() api.LinkableSecretSpec {
			return api.LinkableSecretSpec{
				LinkedTo: []api.SecretLink{
					{
						ServiceAccount: api.ServiceAccountLink{
							Managed: api.ManagedServiceAccountSpec{
								Name: "sa",
							},
						},
					},
				},
			}
		}
		objectMarker.MarkManagedImpl = func(ctx context.Context, k client.ObjectKey, o client.Object) (bool, error) {
			objectMarker.MarkReferenced(ctx, k, o)
			if o.GetLabels() == nil {
				o.SetLabels(map[string]string{})
			}
			o.GetLabels()["gelinkt_managed"] = "yay"
			return true, nil
		}
		currentReferenceName := "o1"
		objectMarker.MarkReferencedImpl = func(ctx context.Context, _ client.ObjectKey, o client.Object) (bool, error) {
			if o.GetAnnotations() == nil {
				o.SetAnnotations(map[string]string{})
			}
			o.GetAnnotations()["gelinkt"] = commaseparated.Value(o.GetAnnotations()["gelinkt"]).Add(currentReferenceName).String()

			return true, nil
		}
		deploymentTarget.GetTargetNamespaceImpl = func() string {
			return "default"
		}

		_, _, err := h.Sync(context.TODO())
		assert.NoError(t, err)

		storedSA := &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKey{Name: "sa", Namespace: "default"}, storedSA))
		assert.Equal(t, "sa", storedSA.Name)
		assert.Equal(t, "yay", storedSA.Labels["gelinkt_managed"])
		assert.Equal(t, "o1", storedSA.Annotations["gelinkt"])

		// now, let's add additional referenced objects
		deploymentTarget.GetSpecImpl = func() api.LinkableSecretSpec {
			return api.LinkableSecretSpec{
				LinkedTo: []api.SecretLink{
					{
						ServiceAccount: api.ServiceAccountLink{
							Reference: corev1.LocalObjectReference{
								Name: "sa",
							},
						},
					},
				},
			}
		}
		currentReferenceName = "o2"

		_, _, err = h.Sync(context.TODO())
		assert.NoError(t, err)

		storedSA = &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKey{Name: "sa", Namespace: "default"}, storedSA))
		assert.Equal(t, "sa", storedSA.Name)
		assert.Equal(t, "yay", storedSA.Labels["gelinkt_managed"])
		assert.Equal(t, "o1,o2", storedSA.Annotations["gelinkt"])

		currentReferenceName = "o3"

		_, _, err = h.Sync(context.TODO())
		assert.NoError(t, err)

		storedSA = &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKey{Name: "sa", Namespace: "default"}, storedSA))
		assert.Equal(t, "sa", storedSA.Name)
		assert.Equal(t, "yay", storedSA.Labels["gelinkt_managed"])
		assert.Equal(t, "o1,o2,o3", storedSA.Annotations["gelinkt"])
	})
}

func TestLinkSecretToServiceAccount(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa",
			Namespace: "default",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret, sa).
		Build()

	secretSpec := api.LinkableSecretSpec{
		LinkedTo: []api.SecretLink{
			{
				ServiceAccount: api.ServiceAccountLink{
					Reference: corev1.LocalObjectReference{
						Name: "sa",
					},
				},
			},
		},
	}

	h := serviceAccountHandler{
		Target: &TestDeploymentTarget{
			GetClientImpl: func() client.Client { return cl },
			GetTargetNamespaceImpl: func() string {
				return "default"
			},
			GetSpecImpl: func() api.LinkableSecretSpec {
				return secretSpec
			},
		},
		ObjectMarker: &TestObjectMarker{},
	}

	t.Run("link as secret", func(t *testing.T) {
		secretSpec.LinkedTo[0].ServiceAccount.As = ""
		h.LinkToSecret(context.TODO(), []*corev1.ServiceAccount{sa}, secret)

		assert.Len(t, sa.Secrets, 1)
		assert.Equal(t, sa.Secrets[0].Name, secret.Name)

		loadedSA := &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKeyFromObject(sa), loadedSA))

		assert.Len(t, loadedSA.Secrets, 1)
		assert.Equal(t, loadedSA.Secrets[0].Name, secret.Name)
	})

	t.Run("link as image pull secret", func(t *testing.T) {
		secretSpec.LinkedTo[0].ServiceAccount.As = api.ServiceAccountLinkTypeImagePullSecret
		h.LinkToSecret(context.TODO(), []*corev1.ServiceAccount{sa}, secret)

		assert.Len(t, sa.ImagePullSecrets, 1)
		assert.Equal(t, sa.ImagePullSecrets[0].Name, secret.Name)

		loadedSA := &corev1.ServiceAccount{}
		assert.NoError(t, cl.Get(context.TODO(), client.ObjectKeyFromObject(sa), loadedSA))

		assert.Len(t, loadedSA.ImagePullSecrets, 1)
		assert.Equal(t, loadedSA.ImagePullSecrets[0].Name, secret.Name)
	})
}

func TestUnlinkSecretFromServiceAccount(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa",
			Namespace: "default",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret, sa).
		Build()

	secretSpec := api.LinkableSecretSpec{
		LinkedTo: []api.SecretLink{
			{
				ServiceAccount: api.ServiceAccountLink{
					Reference: corev1.LocalObjectReference{
						Name: "sa",
					},
				},
			},
		},
	}

	r := serviceAccountHandler{
		Target: &TestDeploymentTarget{
			GetClientImpl: func() client.Client { return cl },
			GetTargetNamespaceImpl: func() string {
				return "default"
			},
			GetSpecImpl: func() api.LinkableSecretSpec {
				return secretSpec
			},
		},
		ObjectMarker: &TestObjectMarker{},
	}

	t.Run("removes only referenced secrets", func(t *testing.T) {
		sa := &corev1.ServiceAccount{
			Secrets: []corev1.ObjectReference{
				{
					Name: "another",
				},
				{
					Name: "secret",
				},
			},
			ImagePullSecrets: []corev1.LocalObjectReference{
				{
					Name: "another",
				},
				{
					Name: "secret",
				},
			},
		}

		changed := r.Unlink(secret, sa)

		assert.True(t, changed)
		assert.Len(t, sa.Secrets, 1)
		assert.Len(t, sa.ImagePullSecrets, 1)

		assert.Equal(t, sa.Secrets[0].Name, "another")
		assert.Equal(t, sa.ImagePullSecrets[0].Name, "another")
	})

	t.Run("doesn't fail if not referenced", func(t *testing.T) {
		sa := &corev1.ServiceAccount{
			Secrets: []corev1.ObjectReference{
				{
					Name: "another",
				},
			},
			ImagePullSecrets: []corev1.LocalObjectReference{
				{
					Name: "another",
				},
			},
		}

		changed := r.Unlink(secret, sa)

		assert.False(t, changed)
		assert.Len(t, sa.Secrets, 1)
		assert.Len(t, sa.ImagePullSecrets, 1)

		assert.Equal(t, sa.Secrets[0].Name, "another")
		assert.Equal(t, sa.ImagePullSecrets[0].Name, "another")
	})

	t.Run("doesn't fail on empty", func(t *testing.T) {
		sa := &corev1.ServiceAccount{}

		changed := r.Unlink(secret, sa)

		assert.False(t, changed)
		assert.Len(t, sa.Secrets, 0)
		assert.Len(t, sa.ImagePullSecrets, 0)
	})
}
