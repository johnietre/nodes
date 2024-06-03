package main

import (
  urlpkg "net/url"

  webs "golang.org/x/net/websocket"
)

type NodeArr = []*Node

type Node struct {
  Name string `json:"name"`
  Addr string `json:"addr,omitempty"`
  IsSecure bool `json:"isSecure,omitempty"`
  IsMaster bool `json:"isMaster,omitempty"`
  IsStatic bool `json:"isStatic,omitempty"`
  ConnectedAt int64 `json:"conectedAt,omitempty"`

  ws *webs.Conn
  ready chan bool
}

func (n *Node) Clone() *Node {
  node := *n
  return &node
}

func (n *Node) setReady(ready bool) {
  if n.ready != nil {
    select {
    case n.ready <- ready:
    default:
    }
  }
}

type Message struct {
  Id uint64 `json:"id,omitempty"`
  Action string `json:"action"`
  Nodes NodeArr `json:"nodes,omitempty"`
  Content any `json:"content,omitempty"`
  Error string `json:"error,omitempty"`

  node *Node
}

const (
  ActionConnect = "connect"
  ActionDisconnect = "disconnect"
  ActionMessage = "message"
  ActionHeartbeat = "heartbeat"
  ActionNewMaster = "new-master"
  ActionResponse = "response"
  ActionError = "error"
)

const (
  NameExists = "name already exists"
  ExpectedNode = "expected one node"
  InvalidPassword = "invalid password"
  PasswordRequired = "password required"
)

func sendActionError(ws *webs.Conn, id uint64, action, msg string) error {
  return webs.JSON.Send(ws, newActionError(id, action, msg))
}

func newActionError(id uint64, action, msg string) Message {
  return Message{Id: id, Action: action, Error: msg}
}

func bubbleNodes(nodes NodeArr) {
  l, swapped := len(nodes), true
  for end := l; swapped && end != 0; end-- {
    swapped = false
    for i := 1; i < end; i++ {
      if nodes[i].ConnectedAt < nodes[i-1].ConnectedAt {
        swapped = true
        nodes[i-1], nodes[i] = nodes[i], nodes[i-1]
      }
    }
  }
}

func GetWsAddr(ws *webs.Conn) string {
  if r := ws.Request(); r != nil {
    return r.RemoteAddr
  }
  url, err := urlpkg.Parse(ws.RemoteAddr().String())
  if err != nil {
    return ""
  }
  return url.Host
}
