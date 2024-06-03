package main

import (
  "html/template"
  "log"
  "net/http"

  utils "github.com/johnietre/utils/go"
)

type HandlerConfig struct {
  Addr string
  Name string
  Static bool
  MasterPassword string
  ServantPassword *string
}

type Handler struct {
  Servant *ServantHandler
  Master *MasterHandler
  router *http.ServeMux

  ThisNode *Node
  MasterPassword string
  Nodes *utils.RWMutex[NodeArr]
}

func NewHandler(config *HandlerConfig) *Handler {
  node := &Node{
    Name: config.Name,
    Addr: config.Addr,
    IsStatic: config.Static,
    IsMaster: true,
  }
  app := &Handler{
    ThisNode: node,
    MasterPassword: config.MasterPassword,
    Nodes: utils.NewRWMutex[NodeArr](NodeArr{node}),
  }

  app.Servant = NewServantHandler(app, config.ServantPassword)
  app.Master = NewMasterHandler(app)

  app.router = http.NewServeMux()
  app.router.HandleFunc("/", app.homeHandler)
  app.router.Handle(
    "/static/",
    http.StripPrefix("/static", http.FileServer(http.Dir("./static"))),
  )
  app.router.Handle("/ws", app.Servant)
  app.router.Handle("/master", app.Master)

  return app
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  h.router.ServeHTTP(w, r)
}

func (h *Handler) homeHandler(w http.ResponseWriter, r *http.Request) {
  type PageData struct {
    Name string
  }

  path := r.URL.Path
  if path != "" && path != "/" {
    http.Error(w, "not found", http.StatusNotFound)
    return
  }
  t := template.New("index.html").Delims("{|", "|}")
  t, err := t.ParseFiles("templates/index.html")
  if err != nil {
    log.Print("Error parsing template: ", err)
    w.WriteHeader(http.StatusInternalServerError)
    return
  }
  if err := t.Execute(w, PageData{Name: h.ThisNode.Name}); err != nil {
    log.Print("Error parsing template: ", err)
  }
}

func (h *Handler) IsMaster() bool {
  defer h.Nodes.RUnlock()
  return h.LockedIsMaster(*h.Nodes.RLock())
}

func (h *Handler) LockedIsMaster(nodes NodeArr) bool {
  // TODO
  for _, node := range nodes {
    if node.Name == h.ThisNode.Name {
      return node.IsMaster
    }
  }
  return true
}

func (h *Handler) ThisAddr() string {
  return h.ThisNode.Addr
}

func (h *Handler) ThisName() string {
  return h.ThisNode.Name
}

func (h *Handler) ResetNodes() {
  h.LockedResetNodes(h.Nodes.Lock())
  h.Nodes.Unlock()
}

func (h *Handler) LockedResetNodes(np *NodeArr) {
  *np = NodeArr{h.ThisNode}
}

type App = Handler
