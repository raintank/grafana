package api

import (
	"github.com/Unknwon/macaron"
	"github.com/grafana/grafana/pkg/api/dtos"
	"github.com/grafana/grafana/pkg/middleware"
	m "github.com/grafana/grafana/pkg/models"
	"github.com/macaron-contrib/binding"
)

// Register adds http routes
func Register(r *macaron.Macaron) {
	reqSignedIn := middleware.Auth(&middleware.AuthOptions{ReqSignedIn: true})
	reqGrafanaAdmin := middleware.Auth(&middleware.AuthOptions{ReqSignedIn: true, ReqGrafanaAdmin: true})
	reqEditorRole := middleware.RoleAuth(m.ROLE_EDITOR, m.ROLE_ADMIN)
	reqAccountAdmin := middleware.RoleAuth(m.ROLE_ADMIN)
	bind := binding.Bind

	// not logged in views
	r.Get("/", reqSignedIn, Index)
	r.Get("/logout", Logout)
	r.Post("/login", bind(dtos.LoginCommand{}), LoginPost)
	r.Get("/login/:name", OAuthLogin)
	r.Get("/login", LoginView)

	// authed views
	r.Get("/profile/", reqSignedIn, Index)
	r.Get("/org/", reqSignedIn, Index)
	r.Get("/datasources/", reqSignedIn, Index)
	r.Get("/datasources/edit/*", reqSignedIn, Index)
	r.Get("/org/users/", reqSignedIn, Index)
	r.Get("/org/apikeys/", reqSignedIn, Index)
	r.Get("/dashboard/import/", reqSignedIn, Index)
	r.Get("/admin/settings", reqGrafanaAdmin, Index)
	r.Get("/admin/users", reqGrafanaAdmin, Index)
	r.Get("/admin/users/create", reqGrafanaAdmin, Index)
	r.Get("/admin/users/edit/:id", reqGrafanaAdmin, Index)
	r.Get("/dashboard/*", reqSignedIn, Index)
	r.Get("/collectors", reqSignedIn, Index)
	r.Get("/endpoints", reqSignedIn, Index)
	// sign up
	r.Get("/signup", Index)
	r.Post("/api/user/signup", bind(m.CreateUserCommand{}), SignUp)

	// dashboard snapshots
	r.Post("/api/snapshots/", bind(m.CreateDashboardSnapshotCommand{}), CreateDashboardSnapshot)
	r.Get("/dashboard/snapshot/*", Index)

	r.Get("/api/snapshots/:key", GetDashboardSnapshot)
	r.Get("/api/snapshots-delete/:key", DeleteDashboardSnapshot)

	// api renew session based on remember cookie
	r.Get("/api/login/ping", LoginApiPing)

	// authed api
	r.Group("/api", func() {
		// user
		r.Group("/user", func() {
			r.Get("/", GetUser)
			r.Put("/", bind(m.UpdateUserCommand{}), UpdateUser)
			r.Post("/using/:id", UserSetUsingOrg)
			r.Get("/orgs", GetUserOrgList)
			r.Post("/stars/dashboard/:id", StarDashboard)
			r.Delete("/stars/dashboard/:id", UnstarDashboard)
			r.Put("/password", bind(m.ChangeUserPasswordCommand{}), ChangeUserPassword)
		})

		// Org
		r.Get("/org", GetOrg)
		r.Group("/org", func() {
			r.Post("/", bind(m.CreateOrgCommand{}), CreateOrg)
			r.Put("/", bind(m.UpdateOrgCommand{}), UpdateOrg)
			r.Post("/users", bind(m.AddOrgUserCommand{}), AddOrgUser)
			r.Get("/users", GetOrgUsers)
			r.Delete("/users/:id", RemoveOrgUser)
		}, reqAccountAdmin)

		// auth api keys
		r.Group("/auth/keys", func() {
			r.Get("/", GetApiKeys)
			r.Post("/", bind(m.AddApiKeyCommand{}), AddApiKey)
			r.Delete("/:id", DeleteApiKey)
		}, reqAccountAdmin)

		// Data sources
		r.Group("/datasources", func() {
			r.Combo("/").
				Get(GetDataSources).
				Put(bind(m.AddDataSourceCommand{}), AddDataSource).
				Post(bind(m.UpdateDataSourceCommand{}), UpdateDataSource)
			r.Delete("/:id", DeleteDataSource)
			r.Get("/:id", GetDataSourceById)
			r.Get("/plugins", GetDataSourcePlugins)
		}, reqAccountAdmin)

		r.Get("/frontend/settings/", GetFrontendSettings)
		r.Any("/datasources/proxy/:id/*", reqSignedIn, ProxyDataSourceRequest)

		// Dashboard
		r.Group("/dashboards", func() {
			r.Combo("/db/:slug").Get(GetDashboard).Delete(DeleteDashboard)
			r.Post("/db", reqEditorRole, bind(m.SaveDashboardCommand{}), PostDashboard)
			r.Get("/home", GetHomeDashboard)
		})

		// Search
		r.Get("/search/", Search)

		// metrics
		r.Get("/metrics/test", GetTestMetrics)

		// collectors
		r.Group("/collectors", func() {
			r.Combo("/").
				Get(bind(m.GetCollectorsQuery{}), GetCollectors).
				Put(reqEditorRole, bind(m.AddCollectorCommand{}), AddCollector).
				Post(reqEditorRole, bind(m.UpdateCollectorCommand{}), UpdateCollector)
			r.Get("/:id/health", getCollectorHealthById)
			r.Get("/:id", GetCollectorById)
			r.Delete("/:id", reqEditorRole, DeleteCollector)
		})

		// Monitors
		r.Group("/monitors", func() {
			r.Combo("/").
				Get(bind(m.GetMonitorsQuery{}), GetMonitors).
				Put(reqEditorRole, bind(m.AddMonitorCommand{}), AddMonitor).
				Post(reqEditorRole, bind(m.UpdateMonitorCommand{}), UpdateMonitor)
			r.Get("/:id/health", getMonitorHealthById)
			r.Get("/:id", GetMonitorById)
			r.Delete("/:id", reqEditorRole, DeleteMonitor)
		})
		// endpoints
		r.Group("/endpoints", func() {
			r.Combo("/").Get(bind(m.GetEndpointsQuery{}), GetEndpoints).
				Put(reqEditorRole, bind(m.AddEndpointCommand{}), AddEndpoint).
				Post(reqEditorRole, bind(m.UpdateEndpointCommand{}), UpdateEndpoint)
			r.Get("/:id/health", getEndpointHealthById)
			r.Get("/:id", GetEndpointById)
			r.Delete("/:id", reqEditorRole, DeleteEndpoint)
			r.Get("/discover", reqEditorRole, bind(m.EndpointDiscoveryCommand{}), DiscoverEndpoint)
		})

		// alerts
		r.Group("/alerts", func() {
			r.Combo("/").Get(bind(m.GetAlertsQuery{}), GetAlerts).
				Put(reqEditorRole, bind(m.AddAlertCommand{}), AddAlert)
			//Post(reqEditorRole, bind(m.UpdateAlertommand{}), UpdateAlert) // TODO
			r.Get("/:id", GetAlertById)
			r.Delete("/:id", reqEditorRole, DeleteAlert)
		})

		r.Get("/monitor_types", GetMonitorTypes)

		//Events
		r.Get("/events", bind(m.GetEventsQuery{}), GetEvents)

		//Get Graph data from Graphite.
		r.Any("/graphite/*", GraphiteProxy)

	}, reqSignedIn)

	// admin api
	r.Group("/api/admin", func() {
		r.Get("/settings", AdminGetSettings)
		r.Get("/users", AdminSearchUsers)
		r.Get("/users/:id", AdminGetUser)
		r.Post("/users", bind(dtos.AdminCreateUserForm{}), AdminCreateUser)
		r.Put("/users/:id/details", bind(dtos.AdminUpdateUserForm{}), AdminUpdateUser)
		r.Put("/users/:id/password", bind(dtos.AdminUpdateUserPasswordForm{}), AdminUpdateUserPassword)
		r.Put("/users/:id/permissions", bind(dtos.AdminUpdateUserPermissionsForm{}), AdminUpdateUserPermissions)
		r.Delete("/users/:id", AdminDeleteUser)
	}, reqGrafanaAdmin)

	// rendering
	r.Get("/render/*", reqSignedIn, RenderToPng)

	r.Any("/socket.io/", SocketIO)

	r.NotFound(NotFound)
}
