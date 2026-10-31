package web

// routes registers every route of the contract (internal/webapi documents
// each body). Each area lives in its own file and registers itself, so the
// areas can be built independently without touching this one:
//
//	routes_session.go  session, pairing, the event stream
//	routes_board.go    the rail
//	routes_card.go     a card: head, thread, live, answer, send, actions
//	routes_docs.go     a card's spec, diff, pull request and stats
//	routes_files.go    a card's worktree files, opened in the browser
//	routes_term.go     a shell in a card's worktree (--terminal)
//	routes_create.go   the new-card form
//	routes_stacks.go   stacks
//	routes_repos.go    repositories and their branches
//	routes_schedules.go  schedules and heartbeats
//	routes_curator.go  the skill pack and its curator
//	routes_profiles.go the board's profiles
//	routes_system.go   doctor and the fleet's stats
//	routes_push.go     Web Push subscriptions
//	routes_attachments.go  image upload and serve
//	routes_spec.go     a session spec's writes
//
// Register authenticated routes with s.api and the few that answer without
// a cookie with s.public. Every write passes the same-origin check in
// Handler whichever it uses. A route not built yet answers notYet (501).
func (s *Server) routes() {
	s.assetRoutes()
	s.sessionRoutes()
	s.boardRoutes()
	s.cardRoutes()
	s.docsRoutes()
	s.specRoutes()
	s.runRoutes()
	s.fileRoutes()
	s.termRoutes()
	s.createRoutes()
	s.stackRoutes()
	s.repoRoutes()
	s.scheduleRoutes()
	s.curatorRoutes()
	s.profilesRoutes()
	s.firstRunRoutes()
	s.systemRoutes()
	s.pushRoutes()
	s.attachmentRoutes()
}
