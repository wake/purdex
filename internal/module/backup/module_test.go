package backup

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModule_NameAndDependencies(t *testing.T) {
	m := New()
	require.Equal(t, "backup", m.Name())
	require.Nil(t, m.Dependencies())
}
