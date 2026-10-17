package gate

// Apple toolchains (#3111): xcodebuild and swift. A test run starts a simulator and about eight cores for every run, a package build
// compiles with several swift-frontend processes of 300 MB each, and none of it went through the gate (air26 reached load 235 with
// one mutation run that started an `xcodebuild test` per mutant). Heavy: xcodebuild unless it only prints something, `swift build`,
// `swift test`, and the driver programs those start (swift-build, swift-test), which a script may also call by their own names.

// xcodebuildInfo are the xcodebuild options that print a text and build nothing.
var xcodebuildInfo = map[string]bool{
	"-list": true, "-version": true, "-showsdks": true, "-showdestinations": true, "-showBuildSettings": true,
	"-showTestPlans": true, "-usage": true, "-help": true, "-license": true, "-checkFirstLaunchStatus": true,
	"-runFirstLaunch": true, "-exportArchive": true, "-exportNotarizedApp": true, "-exportLocalizations": true,
	"-importLocalizations": true, "-downloadAllPlatforms": true, "-downloadPlatform": true,
}

// classifyXcodebuild: whatever xcodebuild is asked to do it builds (the default action is build), unless the line only asks it to print.
func classifyXcodebuild(args []string) Class {
	for _, a := range args {
		if xcodebuildInfo[a] {
			return ClassNone
		}
	}
	return ClassHeavy
}

// classifySwift reads `swift build` and `swift test`. `swift package`, `swift run` (a program the table cannot judge), `swift --version`
// and the rest pass; `--show-bin-path` prints a path and builds nothing.
func classifySwift(args []string) Class {
	i := firstWord(args)
	if i < 0 || (args[i] != "build" && args[i] != "test") {
		return ClassNone
	}
	return classifySwiftDriver(args[i+1:])
}

// classifySwiftDriver is the words after the subcommand (or after swift-build / swift-test, whose name is the subcommand).
func classifySwiftDriver(words []string) Class {
	for _, a := range words {
		switch a {
		case "--show-bin-path", "--help", "-h", "--version", "--list-tests", "-l", "--list-tests-json":
			return ClassNone
		}
	}
	return ClassHeavy
}
