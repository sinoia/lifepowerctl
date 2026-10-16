package jobs

import (
	"fmt"
	"os/user"
	"strconv"
	"strings"
)

const DefaultPath = "/usr/local/bin:/usr/bin:/bin"

// ExecutionIdentity is the fixed ordinary account selected outside the web UI.
// It is presentation data, not a credential or a per-run choice.
type ExecutionIdentity struct {
	User string `json:"user"`
	UID  uint32 `json:"uid"`
	Home string `json:"home"`
	Path string `json:"path"`
}

type IdentityEnvironment struct {
	Home    string
	User    string
	Logname string
	Path    string
}

func (identity ExecutionIdentity) Validate() error {
	if strings.TrimSpace(identity.User) == "" || identity.User == "root" || identity.UID == 0 {
		return fmt.Errorf("job execution user must be an ordinary non-root account")
	}
	if identity.Home == "" || identity.Home[0] != '/' || identity.Path == "" {
		return fmt.Errorf("job execution identity requires an absolute home and fixed PATH")
	}
	return nil
}

func CurrentExecutionIdentity() (ExecutionIdentity, error) {
	account, err := user.Current()
	if err != nil {
		return ExecutionIdentity{}, fmt.Errorf("look up development job user: %w", err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return ExecutionIdentity{}, fmt.Errorf("parse development job user UID: %w", err)
	}
	identity := ExecutionIdentity{User: account.Username, UID: uint32(uid), Home: account.HomeDir, Path: DefaultPath}
	if err := identity.Validate(); err != nil {
		return ExecutionIdentity{}, err
	}
	return identity, nil
}

func (identity ExecutionIdentity) Environment() IdentityEnvironment {
	return IdentityEnvironment{Home: identity.Home, User: identity.User, Logname: identity.User, Path: identity.Path}
}
