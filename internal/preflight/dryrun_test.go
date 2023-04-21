package preflight

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"package-operator.run/package-operator/internal/testutil"
)

var errTest = errors.New("explosion")

func TestCreationDryRun(t *testing.T) {
	c := testutil.NewClient()

	c.
		On("Create", mock.Anything, mock.Anything, mock.Anything).
		Return(errTest)

	obj := &unstructured.Unstructured{}
	obj.SetName("test")
	obj.SetNamespace("test-ns")
	obj.SetKind("Hans")

	dr := NewCreationDryRun(c)
	v, err := dr.Check(context.Background(), obj, obj)
	require.NoError(t, err)
	if assert.Len(t, v, 1) {
		assert.Equal(t, Violation{
			Position: "Hans test-ns/test",
			Error:    "explosion",
		}, v[0])
	}
}

func TestCreationDryRun_alreadyExists(t *testing.T) {
	c := testutil.NewClient()

	c.
		On("Create", mock.Anything, mock.Anything, mock.Anything).
		Return(k8serrors.NewAlreadyExists(schema.GroupResource{}, ""))

	obj := &unstructured.Unstructured{}
	obj.SetName("test")
	obj.SetNamespace("test-ns")
	obj.SetKind("Hans")

	dr := NewCreationDryRun(c)
	v, err := dr.Check(context.Background(), obj, obj)
	require.NoError(t, err)
	assert.Len(t, v, 0)
}
