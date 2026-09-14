package core

// Role is a space-level membership role.
type Role string

const (
	RoleOwner     Role = "owner"
	RoleAdmin     Role = "admin"
	RoleEditor    Role = "editor"
	RoleCommenter Role = "commenter"
	RoleViewer    Role = "viewer"
)

// Roles lists every valid role from most to least privileged.
var Roles = []Role{RoleOwner, RoleAdmin, RoleEditor, RoleCommenter, RoleViewer}

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	for _, k := range Roles {
		if k == r {
			return true
		}
	}
	return false
}

// Access is what an actor may do with a note or space. Higher values include
// every lower value.
type Access int

const (
	AccessNone Access = iota
	AccessView
	AccessComment
	AccessEdit
	AccessManage
)

// Access returns the access level a role carries inside its space.
func (r Role) Access() Access {
	switch r {
	case RoleOwner, RoleAdmin:
		return AccessManage
	case RoleEditor:
		return AccessEdit
	case RoleCommenter:
		return AccessComment
	case RoleViewer:
		return AccessView
	}
	return AccessNone
}

// String returns the wire name of an access level.
func (a Access) String() string {
	switch a {
	case AccessView:
		return "view"
	case AccessComment:
		return "comment"
	case AccessEdit:
		return "edit"
	case AccessManage:
		return "manage"
	}
	return "none"
}

// MarshalText encodes the access level as its wire name.
func (a Access) MarshalText() ([]byte, error) { return []byte(a.String()), nil }

// UnmarshalText decodes a wire name into an access level.
func (a *Access) UnmarshalText(b []byte) error {
	v, ok := ParseAccess(string(b))
	if !ok {
		return Invalid("access", "unknown access level "+string(b))
	}
	*a = v
	return nil
}

// ParseAccess converts a wire name into an access level.
func ParseAccess(s string) (Access, bool) {
	switch s {
	case "none", "":
		return AccessNone, true
	case "view", "read":
		return AccessView, true
	case "comment":
		return AccessComment, true
	case "edit", "write":
		return AccessEdit, true
	case "manage", "admin":
		return AccessManage, true
	}
	return AccessNone, false
}

// AtLeast reports whether a permits everything want permits.
func (a Access) AtLeast(want Access) bool { return a >= want }

// Actor is whoever is making a request. Exactly one of User or Link is set.
// A nil Actor is an anonymous visitor with no access.
type Actor struct {
	User *User
	// Link is set when the request arrived through a public share token.
	Link *PublicLink
	// Token is set when the request authenticated with an API token.
	Token *APIToken
	// Session is set when the request authenticated with a session cookie.
	Session *Session
	// Plugin is set when the request authenticated with a plugin token or an
	// acting token. With a plugin token User is the plugin user. With an
	// acting token User is the person the plugin acts for and Via is set.
	Plugin *PluginIdentity
	// Via is the plugin id of an acting token. Access is then the
	// intersection of the user's access and the plugin's grant.
	Via string
}

// SystemUserID is the user id of the system actor. No users row has it.
const SystemUserID = "system"

// SystemActor returns the actor of a system API token. It has instance admin
// rights and no account, so it cannot sign in, own spaces or create tokens.
func SystemActor(t *APIToken) *Actor {
	return &Actor{
		User:  &User{ID: SystemUserID, Name: t.Name, IsAdmin: true, IsActive: true},
		Token: t,
	}
}

// IsSystem reports whether the actor authenticated with a system API token.
func (a *Actor) IsSystem() bool { return a != nil && a.Token.IsSystem() }

// IsUser reports whether the actor is a signed-in user.
func (a *Actor) IsUser() bool { return a != nil && a.User != nil }

// IsAdmin reports whether the actor is a signed-in instance admin. A plugin
// actor is never an admin, whoever it acts for.
func (a *Actor) IsAdmin() bool { return a.IsUser() && a.Plugin == nil && a.User.IsAdmin }

// IsPlugin reports whether the actor authenticated as a plugin, with a
// plugin token or an acting token.
func (a *Actor) IsPlugin() bool { return a != nil && a.Plugin != nil }

// IsPerson reports whether the actor is a signed-in person acting for
// themselves, not a plugin and not a public link.
func (a *Actor) IsPerson() bool { return a.IsUser() && a.Plugin == nil }

// UserID returns the signed-in user id, or the empty string.
func (a *Actor) UserID() string {
	if !a.IsUser() {
		return ""
	}
	return a.User.ID
}

// Grant is the resolved access an actor has to one note, with the reason.
type Grant struct {
	Access Access `json:"access"`
	// Source is "admin", "space", "share", "inherited-share", "link" or "none".
	Source string `json:"source"`
	Role   Role   `json:"role,omitempty"`
}
