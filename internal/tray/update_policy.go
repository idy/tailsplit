package tray

import (
	"crypto/sha256"
	"fmt"

	"github.com/idy/tailsplit"
)

// updatePolicy binds the source document to the externally loaded domain rules.
// It never refreshes subscriptions: it must compare the files an ordinary restart
// would use with the projection the current helper actually compiled.
type updatePolicy struct {
	data     []byte
	document string
	rules    string
	token    string
}

func readUpdatePolicy(path string) (updatePolicy, error) {
	data, revision, err := ReadConfigDocument(path)
	if err != nil {
		return updatePolicy{}, err
	}
	config, err := startupConfig(path, data)
	if err != nil {
		return updatePolicy{}, err
	}
	policy := updatePolicy{data: data, document: revision, token: revision}
	if config.Rules.Mode == tailsplit.RoutingGlobal || config.Rules.GFWList == "" {
		return policy, nil
	}
	policy.rules, err = tailsplit.CurrentRulesRevision(config)
	if err != nil {
		return updatePolicy{}, err
	}
	policy.token = fmt.Sprintf("%x", sha256.Sum256([]byte(revision+"\x00"+policy.rules)))
	return policy, nil
}

// UpdateRestartRevision returns the one-time policy identity passed through the
// installer. A live session must agree with both the saved document and the
// helper-reported rule projection; a stopped app can update an invalid profile.
func UpdateRestartRevision(path string, snapshot Snapshot) (string, error) {
	traffic := snapshot.Active && (snapshot.Mode == "tun" || snapshot.Mode == "proxy") &&
		snapshot.State != "stopping" && snapshot.State != "failed" && snapshot.State != "logging-out"
	if traffic && snapshot.RoutingMode == tailsplit.RoutingGlobal {
		return "", errUpdateGlobal
	}
	policy, err := readUpdatePolicy(path)
	if err != nil {
		if traffic {
			return "", err
		}
		return "", nil
	}
	if traffic {
		if snapshot.ActiveRevision == "" || snapshot.ActiveRevision != policy.document {
			return "", ErrConfigChanged
		}
		if policy.rules != "" && policy.rules != snapshot.ActiveRulesRevision {
			return "", tailsplit.ErrRulesChanged
		}
	}
	return policy.token, nil
}

func (c *Controller) checkUpdatePolicyLocked(expected string) error {
	policy, err := readUpdatePolicy(c.options.ConfigPath)
	if err != nil {
		return err
	}
	if expected == "" || policy.token != expected || policy.document != c.activeRevision {
		return ErrConfigChanged
	}
	if policy.rules != "" && policy.rules != c.snapshot.ActiveRulesRevision {
		return tailsplit.ErrRulesChanged
	}
	return nil
}

// StartUpdateRevision captures the matching YAML document and passes its expected
// rule projection to the helper. The helper checks its final compiled projection
// before traffic starts, closing the race with mutable rule files/subscriptions.
func (c *Controller) StartUpdateRevision(expected string) error {
	return c.startUpdateRevision(expected, "")
}

func (c *Controller) startUpdateRevision(expected, operation string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.operationAllowedLocked(operation); err != nil {
		return err
	}
	if c.reconfiguring {
		return tailsplit.ErrPingBusy
	}
	if err := c.checkStartLocked(); err != nil {
		return err
	}
	policy, err := readUpdatePolicy(c.options.ConfigPath)
	if err != nil {
		return err
	}
	if expected == "" || policy.token != expected {
		return ErrConfigChanged
	}
	if err := c.startConnectionLocked("proxy", "", policy.data, policy.document, policy.rules); err != nil {
		return err
	}
	c.ownOperationLocked(operation)
	return nil
}
