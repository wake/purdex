package daemon

import "testing"

// S3-3b-2: [advisor] queue_rules is off unless it is set, and is its own switch.
func TestAdvisorQueueRulesAreOffByDefault(t *testing.T) {
	c, err := loadAdvisor(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Advisor.QueueRules {
		t.Fatalf("defaults: %+v", c.Advisor)
	}
}

func TestAdvisorQueueRulesOnIsIndependentOfWatchRules(t *testing.T) {
	c, err := loadAdvisor(t, "[advisor]\nqueue_rules = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Advisor.QueueRules || c.Advisor.WatchRules {
		t.Fatalf("%+v", c.Advisor)
	}
	c, err = loadAdvisor(t, "[advisor]\nwatch_rules = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Advisor.QueueRules || !c.Advisor.WatchRules {
		t.Fatalf("%+v", c.Advisor)
	}
}

// A misspelt key is still refused (the config is read strictly), so a typo in queue_rules is not a rule quietly left off.
func TestAdvisorQueueRulesMisspeltIsRefused(t *testing.T) {
	if _, err := loadAdvisor(t, "[advisor]\nqueue_rule = true\n"); err == nil {
		t.Error("a misspelt key was accepted")
	}
}
