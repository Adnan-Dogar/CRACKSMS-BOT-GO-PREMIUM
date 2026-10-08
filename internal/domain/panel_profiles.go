package domain

type PanelProfile struct {
	Name, Category, APIPath string
	Numbers, Statistics     bool
}

func Profile(kind string) PanelProfile {
	switch kind {
	case "login":
		return PanelProfile{Name: "Login Panel", Category: "Login Panel"}
	case "token_api":
		return PanelProfile{Name: "CR API", Category: "CR API"}
	case "legacy_api":
		return PanelProfile{Name: "Reseller API", Category: "Reseller API"}
	case "ivas":
		return PanelProfile{Name: "IVAS Account", Category: "Live SMS Stream"}
	case "socketio":
		return PanelProfile{Name: "Socket.IO", Category: "Live SMS Stream"}
	case "websocket":
		return PanelProfile{Name: "Plain WebSocket", Category: "Live SMS Stream"}
	case "axon_asp":
		return PanelProfile{Name: "ASP SMS API", Category: "ASP SMS API", APIPath: "/api/sms", Statistics: true}
	case "augestel":
		return PanelProfile{Name: "IPRN REST API", Category: "IPRN REST API", APIPath: "/api/v1/iprn", Numbers: true, Statistics: true}
	}
	return PanelProfile{Name: kind, Category: kind}
}
