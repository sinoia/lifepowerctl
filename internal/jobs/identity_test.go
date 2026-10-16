package jobs

import "testing"

func TestExecutionIdentityValidation(t *testing.T) {
	valid := ExecutionIdentity{User: "pi", UID: 1000, Home: "/home/pi", Path: DefaultPath}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if environment := valid.Environment(); environment.User != "pi" || environment.Logname != "pi" || environment.Home != "/home/pi" {
		t.Fatalf("environment = %+v", environment)
	}
	for _, invalid := range []ExecutionIdentity{
		{User: "root", UID: 0, Home: "/root", Path: DefaultPath},
		{User: "pi", UID: 1000, Home: "home/pi", Path: DefaultPath},
		{User: "pi", UID: 1000, Home: "/home/pi"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("accepted invalid identity: %+v", invalid)
		}
	}
}
