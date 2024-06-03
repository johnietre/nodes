package main
// TODO: What to do when passing off master or when receiving master

import (
  "encoding/json"
  "fmt"
  "log"
  "net/http"
  //"sync/atomic"

  utils "github.com/johnietre/utils/go"
  webs "golang.org/x/net/websocket"
)

type WsMsg struct {
  Ws *webs.Conn
  Msg Message
}

type ServantHandler struct {
  app *App
  conns *utils.SyncSet[*webs.Conn]
  msgCh chan WsMsg
  password *string

  //masterWs atomic.Pointer[webs.Conn]
  masterWs *utils.RWMutex[*webs.Conn]
  masterMsgCh chan WsMsg
}

func NewServantHandler(app *Handler, password *string) *ServantHandler {
  h := &ServantHandler{
    app: app,
    conns: utils.NewSyncSet[*webs.Conn](),
    msgCh: make(chan WsMsg, 10),
    password: password,
    masterMsgCh: make(chan WsMsg, 10),
    masterWs: utils.NewRWMutex[*webs.Conn](nil),
  }
  go h.run()
  go h.runMaster()
  return h
}

func (h *ServantHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  webs.Handler(h.handler).ServeHTTP(w, r)
}

func (h *ServantHandler) handler(ws *webs.Conn) {
  defer ws.Close()
  if h.password != nil {
    sendActionError(ws, 0, ActionConnect, PasswordRequired)
    password := *h.password
    for {
      var msg Message
      if err := webs.JSON.Receive(ws, &msg); err != nil {
        return
      }
      if msg.Action != ActionConnect {
        sendActionError(ws, msg.Id, ActionError, "expected action connect")
        continue
      } else if pwd, ok := msg.Content.(string); !ok {
        sendActionError(ws, msg.Id, ActionConnect, "expected password")
        continue
      } else if pwd != password {
        sendActionError(ws, msg.Id, ActionConnect, InvalidPassword)
        continue
      }
      break
    }
  }
  webs.JSON.Send(ws, Message{Action: ActionConnect, Content: h.app.ThisName()})
  h.conns.Insert(ws)
  defer h.conns.Remove(ws)
  h.app.Nodes.RApply(func(np *NodeArr) {
    webs.JSON.Send(ws, Message{
      Action: ActionHeartbeat,
      Nodes: *np,
    })
  })
  for {
    var msg Message
    if err := webs.JSON.Receive(ws, &msg); err != nil {
      break
    }
    h.msgCh <- WsMsg{Ws: ws, Msg: msg}
  }
}

func (h *ServantHandler) run() {
  for wsMsg := range h.msgCh {
    switch wsMsg.Msg.Action {
    case ActionConnect:
      h.handleConnect(wsMsg)
    case ActionDisconnect:
      h.handleDisconnect(wsMsg)
    case ActionMessage:
      h.handleMessage(wsMsg)
    case ActionNewMaster:
      h.handleNewMaster(wsMsg)
    default:
    }
  }
}

func (h *ServantHandler) handleConnect(wsMsg WsMsg) {
  sendErr := func(errMsg string) {
    if wsMsg.Ws != nil {
      sendActionError(wsMsg.Ws, msg.Id, ActionConnect, errMsg)
    } else {
      // Came from master
      // TODO
    }
  }
  //ws := h.MasterWs()
  thisAddr := h.app.ThisAddr()
  wsp := h.masterWs.Lock()
  defer h.masterWs.Unlock()
  ws := *wsp
  if ws != nil {
    if GetWsAddr(ws) != thisAddr {
      sendErr("Already connected to a master, disconnect first")
      return
    }
    ws.Close()
  }
  addr, _ := wsMsg.Msg.Content.(string)
  if addr == "" {
    sendErr("Missing address")
    return
  }
  if addr == thisAddr {
    sendErr("Attempted to connect to self")
    return
  }
  err := h.lockedConnectMaster(wsp, addr, h.app.ThisNode.IsSecure)
  if err != nil {
    sendErr("Error connecting: "+err.Error())
    return
  }
  // NOTE: No need to send message?
}

func (h *ServantHandler) handleDisconnect(wsMsg WsMsg) {
  //ws := h.MasterWs()
  wsp := h.masterWs.Lock()
  defer h.masterWs.Unlock()
  ws := *wsp
  thisAddr := h.app.ThisAddr()
  if ws == nil || GetWsAddr(ws) == thisAddr {
    sendActionError(
      wsMsg.Ws, msg.Id,
      ActionDisconnect, "Not connected",
    )
    return
  }
  ws.Close()
  // Reset nodes since they will be set with new connect
  h.app.ResetNodes()
  err := h.lockedConnectMaster(wsp, thisAddr, h.app.ThisNode.IsSecure)
  if err != nil {
    sendActionError(
      wsMsg.Ws, msg.Id, ActionDisconnect,
      "Error disconnecting (connecting to self): "+err.Error(),
    )
    return
  }
  h.Broadcast(Message{
    Action: ActionDisconnect,
    // TODO
  })
}

func (h *ServantHandler) handleMessage(wsMsg WsMsg) {
  if err := h.sendMaster(wsMsg.Msg); err != nil {
    log.Print("Error sending to master: ", err)
  }
}

func (h *ServantHandler) handleNewMaster(wsMsg WsMsg) {
  msg := wsMsg.Msg
  msg.node = &Node{ws: ws}
  if !h.app.Master.SendMsg(msg) {
    sendActionError(ws, 0, ActionNewMaster, "not master")
  }
}

func (h *ServantHandler) Broadcast(msg Message) {
  msgBytes, _ := json.Marshal(msg)
  h.conns.Range(func(ws *webs.Conn) bool {
    //webs.JSON.Send(ws, msg)
    ws.Write(msgBytes)
    return true
  })
}

func (h *ServantHandler) sendMaster(msg Message) error {
  ws := h.MasterWs()
  if ws == nil {
    return fmt.Errorf("master not connected")
  }
  return webs.JSON.Send(ws, msg)
}

func (h *ServantHandler) ConnectMaster(addr string, secure bool) error {
  defer h.masterWs.Unlock()
  return h.lockedConnectMaster(h.masterWs.Lock(), addr, secure)
}

func (h *ServantHandler) lockedConnectMaster(
  wsp **webs.Conn, addr string, secure bool,
) error {
  return h.internalConnectMaster(wsp, addr, secure, nil)
}

func (h *ServantHandler) internalConnectMaster(
  wsp **webs.Conn, addr string, secure bool, triedAddrs []string,
) error {
  if utils.SearchSlice(triedAddrs, addr) != -1 {
    return errMasterCycle
  }
  // Connect
  var proto, oproto string
  if secure {
    proto, oproto = "wss", "https"
  } else {
    proto, oproto = "ws", "http"
  }
  ws, err := webs.Dial(proto+"://"+addr+"/master", "", oproto+"://127.0.0.1")
  if err != nil {
    return err
  }
  shouldClose := utils.NewT(true)
  defer utils.DeferClose(shouldClose, ws)

  var msg Message
  // Wait for ok
  if err := webs.JSON.Receive(ws, &msg); err != nil {
    return err
  }
  if msg.Action != ActionConnect {
    if msg.Action == ActionNewMaster {
      addr = msg.Error
      return h.internalConnectMaster(
        wsp, addr,
        secure, append(triedAddrs, addr),
      )
    } else if msg.Action == ActionError {
      return fmt.Errorf("received error: %s", msg.Action, msg.Error)
    } else {
      return fmt.Errorf("unexpected action: %s (msg: %+v)", msg.Action, msg)
    }
  }

  // Send this node
  msg = Message{
    Action: ActionConnect,
    Content: h.app.MasterPassword,
    Nodes: NodeArr{h.app.ThisNode},
  }
  if err := webs.JSON.Send(ws, msg); err != nil {
    return err
  }
  // Get response
  msg = Message{}
  if err := webs.JSON.Receive(ws, &msg); err != nil {
    return err
  }
  switch msg.Action {
  case ActionConnect:
    if msg.Error != "" {
      return fmt.Errorf("connect error: %s", msg.Error)
    }
    // TODO: IsMaster
    h.app.Nodes.Apply(func(np *NodeArr) {
      *np = msg.Nodes
    })
  case ActionError:
    return fmt.Errorf("received error: %s", msg.Error)
  default:
    return fmt.Errorf("unexpected action: %s (msg: %+v)", msg.Action, msg)
  }
  *shouldClose = false
  //h.masterWs.Store(ws)
  *wsp = ws
  go h.listenMaster(ws)
  return nil
}

func (h *ServantHandler) runMaster() {
  for wsMsg := range h.masterMsgCh {
    if wsMsg.Ws != h.MasterWs() {
      continue
    }
    msg := wsMsg.Msg
    switch msg.Action {
    case ActionConnect:
      h.handleMasterConnect(msg)
    case ActionDisconnect:
      h.handleMasterDisconnect(msg)
    case ActionMessage:
      h.handleMasterMessage(msg)
    case ActionNewMaster:
      h.handleMasterNewMaster(msg)
    case ActionHeartbeat:
      h.handleMasterHeartbeat(msg)
    case ActionError:
      // TODO
    default:
      // TODO
    }
  }
}

func (h *ServantHandler) listenMaster(ws *webs.Conn) {
  defer ws.Close()
  println("listening", GetWsAddr(ws))
  if GetWsAddr(ws) == h.app.ThisAddr() {
    h.app.Master.Start()
    defer h.app.Master.Stop()
  }
  // Listen
  for {
    var msg Message
    if err := webs.JSON.Receive(ws, &msg); err != nil {
      break
    }
    h.masterMsgCh <- WsMsg{Ws: ws, Msg: msg}
  }
  wsp := h.masterWs.Lock()
  if *wsp != ws {
    // No need to do anything else since the master has already been chanegd
    return
  }
  // Remove master and get new one
  var master *Node
  h.app.Nodes.Apply(func(np *NodeArr) {
    for i, node := range *np {
      if node.IsMaster {
        h.Broadcast(Message{
          Action: ActionDisconnect,
          Content: node.Name,
          Nodes: NodeArr{node},
        })
        *np = append((*np)[:i], (*np)[i+1:]...)
        break
      }
    }
    // Get new master
    master = h.LockedFindNextMaster(*np)
    // Reset nodes since they will be reset on connect
    h.app.LockedResetNodes(np)
  })
  err := h.lockedConnectMaster(wsp, master.Addr, master.IsSecure)
  if err != nil {
    h.Broadcast(Message{
      Action: ActionConnect,
      Error: "Error connecting to new master: "+err.Error(),
    })
    return
  }
}

func (h *ServantHandler) handleMasterConnect(msg Message) {
  h.app.Nodes.Apply(func(np *NodeArr) {
    *np = append(*np, msg.Nodes[0])
    bubbleNodes(*np)
  })
  h.Broadcast(Message{
    Action: ActionConnect,
    Nodes: NodeArr{msg.Nodes[0]},
  })
}

func (h *ServantHandler) handleMasterDisconnect(msg Message) {
  name, _ := msg.Content.(string)
  if name == "" {
    // TODO?
    log.Printf("Missing or empty name in disconnect msg content: %+v", msg)
    return
  }
  found := false
  h.app.Nodes.Apply(func(np *NodeArr) {
    for i, node := range *np {
      if node.Name == name {
        found = true
        *np = append((*np)[:i], (*np)[i+1:]...)
        break
      }
    }
  })
  if found {
    h.Broadcast(Message{
      Action: ActionDisconnect,
      Nodes: msg.Nodes,
      Content: name,
    })
  }
}

func (h *ServantHandler) handleMasterMessage(msg Message) {
  h.Broadcast(msg)
}

func (h *ServantHandler) handleMasterNewMaster(msg Message) {
  h.msgCh <- WsMsg{Msg: msg}
}

func (h *ServantHandler) handleMasterHeartbeat(msg Message) {
  h.app.Nodes.Apply(func(np *NodeArr) {
    *np = msg.Nodes
    h.Broadcast(Message{
      Action: ActionHeartbeat,
      Nodes: *np,
    })
  })
}

func (h *ServantHandler) FindNextMaster() *Node {
  defer h.app.Nodes.RUnlock()
  return h.LockedFindNextMaster(*h.app.Nodes.RLock())
}

func (h *ServantHandler) LockedFindNextMaster(nodes NodeArr) *Node {
  for _, node := range nodes {
    if node.IsStatic {
      return node
    }
  }
  return h.app.ThisNode
}

func (h *ServantHandler) MasterWs() *webs.Conn {
  //return h.masterWs.Load()
  ws := *h.masterWs.Lock()
  defer h.masterWs.Unlock()
  return ws
}

var (
  errMasterCycle = fmt.Errorf("cycle detected when searching for master")
)
