package pool

// Server is one CS2 dedicated server the app can hand to a lobby.
type Server struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	RCONPassword string `json:"rcon_password"`
}
