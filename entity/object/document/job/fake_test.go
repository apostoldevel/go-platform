package job

import "github.com/apostoldevel/go-platform/lib/problem"

// errFakeCreated stands in for a database answer in unit tests.
var errFakeCreated = problem.New(422, "fake", "Fake", "unit test")
