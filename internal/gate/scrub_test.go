package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// What the holder record keeps of a command line (readable by every user, printed by status).
// (M: look only for "token=" inside a word.)
func TestScrubArgv(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []string
		want []string
	}{
		{"nothing to hide", []string{"go", "test", "-race", "./..."}, []string{"go", "test", "-race", "./..."}},
		{"--token abc", []string{"deploy", "--token", "abc", "--env", "prod"}, []string{"deploy", "--token", "<redacted>", "--env", "prod"}},
		{"--token=abc", []string{"deploy", "--token=abc"}, []string{"deploy", "--token=<redacted>"}},
		{"--password hunter2", []string{"login", "--password", "hunter2"}, []string{"login", "--password", "<redacted>"}},
		{"--api-key and --apikey", []string{"x", "--api-key", "k1", "--apikey=k2"}, []string{"x", "--api-key", "<redacted>", "--apikey=<redacted>"}},
		{"--access-token", []string{"x", "--access-token", "t"}, []string{"x", "--access-token", "<redacted>"}},
		{"a header", []string{"curl", "-H", "Authorization: Bearer abc", "https://x"}, []string{"curl", "-H", "<redacted>", "https://x"}},
		{"a long header flag", []string{"curl", "--header", "X-Api-Key: abc"}, []string{"curl", "--header", "<redacted>"}},
		{"a harmless header", []string{"curl", "-H", "Accept: text/html"}, []string{"curl", "-H", "Accept: text/html"}},
		{"a cookie header", []string{"curl", "-H", "Cookie: sid=1"}, []string{"curl", "-H", "<redacted>"}},
		{"an environment word", []string{"env", "API_TOKEN=abc", "go", "test"}, []string{"env", "API_TOKEN=<redacted>", "go", "test"}},
		{"a bearer in a word (the transcript scrubber)", []string{"curl", "Authorization: Bearer abc"}, []string{"curl", "<redacted>"}},
		{"a bearer elsewhere in a word", []string{"curl", "x=Bearer abc.def"}, []string{"curl", "x=[redacted]"}},
		{"an sk- key (the transcript scrubber)", []string{"run", "sk-ant-api03-AbCdEf123456"}, []string{"run", "[redacted]"}},
		{"a hosting token in a URL", []string{"push", "https://x/ghp_AbCdEf1234567890"}, []string{"push", "https://x/[redacted]"}},
		{"a long mixed run", []string{"run", "aB3aB3aB3aB3aB3aB3aB3aB3aB3aB3aB3aB3"}, []string{"run", "[redacted]"}},
		{"--password=abc", []string{"login", "--password=abc"}, []string{"login", "--password=<redacted>"}},
		{"-p abc after mysql", []string{"mysql", "-u", "root", "-p", "abc", "db"}, []string{"mysql", "-u", "root", "-p", "<redacted>", "db"}},
		{"-p abc after a path to psql", []string{"/usr/bin/psql", "-p", "abc"}, []string{"/usr/bin/psql", "-p", "<redacted>"}},
		{"go test -p 4 is not a password", []string{"go", "test", "-p", "4", "./..."}, []string{"go", "test", "-p", "4", "./..."}},
		{"tsc -p is a project", []string{"tsc", "-p", "tsconfig.json"}, []string{"tsc", "-p", "tsconfig.json"}},
		{"docker -p is a port", []string{"docker", "run", "-p", "80:80", "img"}, []string{"docker", "run", "-p", "80:80", "img"}},
		{"a password in a URL", []string{"git", "clone", "https://user:s3cret@host/repo.git"}, []string{"git", "clone", "https://user:<redacted>@host/repo.git"}},
		{"a URL without one", []string{"curl", "https://user@host/x"}, []string{"curl", "https://user@host/x"}},
		{"-Hname: value glued", []string{"curl", "-HAuthorization: Basic dXNlcjpwYXNz"}, []string{"curl", "-H<redacted>"}},
		{"--header=name:value", []string{"curl", "--header=Cookie:sid=abc"}, []string{"curl", "--header=<redacted>"}},
		{"--header=harmless", []string{"curl", "--header=Accept: text/html"}, []string{"curl", "--header=Accept: text/html"}},
		{"-psecret glued after mysql", []string{"mysql", "-phunter2", "db"}, []string{"mysql", "-p<redacted>", "db"}},
		{"-pNNN after go is not a password", []string{"go", "test", "-p4", "./..."}, []string{"go", "test", "-p4", "./..."}},
		{"a Basic header in its own word", []string{"curl", "-H", "Authorization: Basic dXNlcjpwYXNz"}, []string{"curl", "-H", "<redacted>"}},
		{"a flag that only looks alike", []string{"go", "test", "-run", "TestAuthor"}, []string{"go", "test", "-run", "TestAuthor"}},
		{"a secret flag at the end", []string{"x", "--token"}, []string{"x", "--token"}},
	} {
		in := append([]string(nil), c.in...)
		assert.Equal(t, c.want, scrubArgv(c.in), c.name)
		assert.Equal(t, in, c.in, c.name+": the input is not modified")
	}
}
