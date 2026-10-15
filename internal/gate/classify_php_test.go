package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// PHP (lead 10-15, csp): a whole test run is heavy, a filtered or file-targeted one light, install/update/build and the rest none.
func TestClassify_PHP(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		want Class
	}{
		// composer: test scripts
		{"composer test", ClassHeavy},
		{"composer test:integration", ClassHeavy},
		{"composer test:unit", ClassHeavy},
		{"composer run test", ClassHeavy},
		{"composer run-script test", ClassHeavy},
		{"composer run-script test:integration", ClassHeavy},
		{"composer run tests", ClassHeavy},
		{"composer run --timeout=0 test:all", ClassHeavy},
		{"composer -d /work/csp test", ClassHeavy},
		{"composer --working-dir /work/csp test:integration", ClassHeavy},
		{"composer --no-interaction test", ClassHeavy},
		{"composer Test", ClassHeavy}, // composer's script names are case sensitive; the guess errs to the heavy side
		{"composer run-script Test:Integration", ClassHeavy},
		{"composer TESTS", ClassHeavy},
		{"composer --file composer.json test", ClassHeavy},
		{"composer -f composer.json test:integration", ClassHeavy},
		{"composer --unknown-opt other/composer.json test", ClassHeavy},
		{"composer -f composer.json install", ClassNone},
		// ... narrowed by what is handed on to the script
		{"composer test -- --filter FooTest", ClassLight},
		{"composer test -- --filter=FooTest", ClassLight},
		{"composer test:integration -- tests/Integration/FooTest.php", ClassLight},
		{"composer run test -- --filter Foo", ClassLight},
		{"composer test -- --testsuite integration", ClassHeavy},
		{"composer test --", ClassHeavy},
		{"composer test -- --colors", ClassHeavy},
		// composer: never held back
		{"composer install", ClassNone},
		{"composer update", ClassNone},
		{"composer require vendor/pkg", ClassNone},
		{"composer remove vendor/pkg", ClassNone},
		{"composer dump-autoload", ClassNone},
		{"composer dump-autoload -o", ClassNone},
		{"composer build", ClassNone},
		{"composer run build", ClassNone},
		{"composer run-script post-install-cmd", ClassNone},
		{"composer validate", ClassNone},
		{"composer show", ClassNone},
		{"composer --version", ClassNone},
		{"composer", ClassNone},
		{"composer run", ClassNone},
		{"composer -d", ClassNone},
		// composer exec runs a vendor binary: that binary's class
		{"composer exec phpunit", ClassHeavy},
		{"composer exec phpunit -- --filter Foo", ClassLight},
		{"composer exec pest -- tests/Feature/FooTest.php", ClassLight},
		{"composer exec php-cs-fixer", ClassNone},
		{"composer exec", ClassNone},
		// phpunit / pest, also by path
		{"phpunit", ClassHeavy},
		{"vendor/bin/phpunit", ClassHeavy},
		{"./vendor/bin/phpunit", ClassHeavy},
		{"vendor/bin/pest", ClassHeavy},
		{"vendor/bin/phpunit --testsuite integration", ClassHeavy},
		{"vendor/bin/phpunit --testsuite=integration", ClassHeavy},
		{"vendor/bin/phpunit --coverage-html build/cov", ClassHeavy},
		{"vendor/bin/phpunit -c phpunit.xml.dist", ClassHeavy},
		{"vendor/bin/phpunit --configuration phpunit.xml --group slow", ClassHeavy},
		{"vendor/bin/phpunit --filter FooTest", ClassLight},
		{"vendor/bin/phpunit --filter=FooTest", ClassLight},
		{"vendor/bin/phpunit --testsuite integration --filter FooTest", ClassLight},
		{"vendor/bin/phpunit tests/Integration/FooTest.php", ClassLight},
		{"vendor/bin/phpunit tests/Integration", ClassLight},
		{"vendor/bin/phpunit --colors=always tests/Unit/BarTest.php", ClassLight},
		{"vendor/bin/phpunit -c phpunit.xml tests/Unit", ClassLight},
		{"vendor/bin/phpunit -- tests/Unit", ClassLight},
		{"vendor/bin/pest --filter foo", ClassLight},
		{"vendor/bin/pest tests/Feature/FooTest.php", ClassLight},
		{"vendor/bin/pest --parallel", ClassHeavy},
		// values of options that take one are not test paths
		{"vendor/bin/pest --test-directory tests/Feature", ClassHeavy},
		{"vendor/bin/phpunit --exclude-testsuite integration", ClassHeavy},
		{"vendor/bin/phpunit --exclude-filter FooTest", ClassHeavy},
		{"vendor/bin/pest --requires-php-extension pcov", ClassHeavy},
		{"vendor/bin/pest --issue 123", ClassHeavy},
		{"vendor/bin/pest --pr 45", ClassHeavy},
		{"vendor/bin/pest --pull-request 45", ClassHeavy},
		{"vendor/bin/phpunit --columns 120", ClassHeavy},
		// an option this table does not know, followed by a word that is no path: the run is as wide as it was
		{"vendor/bin/phpunit --some-new-option value", ClassHeavy},
		{"vendor/bin/phpunit --some-new-option value tests/Unit/FooTest.php", ClassLight},
		{"vendor/bin/pest Feature", ClassHeavy},
		{"vendor/bin/phpunit -- value", ClassHeavy},
		{"vendor/bin/pest --coverage --min=80", ClassHeavy},
		{"vendor/bin/phpunit --version", ClassNone},
		{"vendor/bin/phpunit --help", ClassNone},
		{"vendor/bin/phpunit --list-tests", ClassNone},
		{"vendor/bin/phpunit --list-suites", ClassNone},
		{"vendor/bin/phpunit --generate-configuration", ClassNone},
	} {
		assert.Equal(t, c.want, Classify(words(c.cmd), Env{}), c.cmd)
	}
}

// Herd keeps composer in a directory with a space in its name: the class goes by the file's name.
func TestClassify_PHPPathWithASpace(t *testing.T) {
	herd := "/Users/wake/Library/Application Support/Herd/bin/composer"
	assert.Equal(t, ClassHeavy, Classify([]string{herd, "test:integration"}, Env{}))
	assert.Equal(t, ClassNone, Classify([]string{herd, "install"}, Env{}))
	assert.Equal(t, ClassLight, Classify([]string{herd, "test", "--", "--filter", "Foo Bar"}, Env{}))
	assert.Equal(t, ClassLight, Classify([]string{"/my project/vendor/bin/phpunit", "tests/Unit/A Test.php"}, Env{}))
}

// The settings' tool list decides what is classified at all: a PHP tool is in it by default, and out when the person's list
// leaves it out.
func TestInterceptClassify_PHPToolsAreIn(t *testing.T) {
	c := InterceptConfig{Tools: defaultTools}
	assert.Equal(t, ClassHeavy, c.Classify(words("composer test"), Env{}))
	assert.Equal(t, ClassHeavy, c.Classify(words("./vendor/bin/phpunit"), Env{}))
	assert.Equal(t, ClassLight, c.Classify(words("vendor/bin/pest --filter x"), Env{}))
	assert.Equal(t, ClassNone, c.Classify(words("composer install"), Env{}))
	out := InterceptConfig{Tools: []string{"go"}}
	assert.Equal(t, ClassNone, out.Classify(words("composer test"), Env{}))
	// a rule can still say more
	r := InterceptConfig{Tools: defaultTools, Rules: []Rule{{Match: "composer build", Class: ClassHeavy}}}
	assert.Equal(t, ClassHeavy, r.Classify(words("composer build"), Env{}))
}
