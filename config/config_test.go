package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parse(t *testing.T, b string) *Config {
	t.Helper()

	config := NewConfig()
	_, err := config.LoadString(b)
	require.NoError(t, err)
	return config
}

func TestProgramConfig(t *testing.T) {
	config := parse(t, `
programs:
  - name: test
    command: /bin/ls
`)

	progs := config.Programs()
	assert.Len(t, progs, 1)
	assert.NotNil(t, config.GetProgram("test"))
	assert.Nil(t, config.GetProgram("app"))
}

func TestHttpServer(t *testing.T) {
	config := parse(t, `
programs:
  - name: test
    command: ls
    A: 1024
    B: 2KB
    C: 3MB
    D: 4GB
    E: test
http_server:
  port: 9898`)

	entry := config.HttpServer
	assert.NotNil(t, entry)
	assert.Equal(t, "9898", entry.Port)
}

func TestProgramInGroup(t *testing.T) {
	config := parse(t, `
programs:
  - name: test1
    command: ls
    A: 123

  - name: test2
    command: ls
    B: hello

  - name: test3
    command: ls
    C: tt

groups:
  - name: test
    programs: 
      - test1
      - test2`)
	require.NotNil(t, config.GetProgram("test1"))
	assert.Equal(t, "test", config.GetProgram("test1").Group)
}
