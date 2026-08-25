package auth

const (
	CapAdmin           = "is_admin"
	CapManageUsers     = "can_manage_users"
	CapManageLibrary   = "can_manage_library"
	CapAutoApprove     = "auto_approve"
	CapCreatePlaylists = "can_create_playlists"
)

type Capability struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// AllCapabilities is the fixed registry, in display order. The single owner
// holds every one of them.
func AllCapabilities() []Capability {
	return []Capability{
		{CapAdmin, "Full administrator", "Complete access; bypasses all restrictions."},
		{CapManageUsers, "Manage users & roles", "Create and edit users, edit roles, and control registration & invites."},
		{CapManageLibrary, "Manage library & integrations", "Configure the music backend, search providers, and downloaders."},
		{CapAutoApprove, "Auto-approve downloads", "One-click downloads are fulfilled immediately."},
		{CapCreatePlaylists, "Create & edit playlists", "Make and manage playlists."},
	}
}
