package main

import (
  "encoding/json"
  "fmt"
  "net/http"
  "sync/atomic"
  "time"

  utils "github.com/johnietre/utils/go"
  webs "golang.org/x/net/websocket"
)

type MasterHandler struct {
  app *App
  runner atomic.Pointer[masterRunner]
}

func NewMasterHandler(app *Handler) *MasterHandler {
  h := &MasterHandler{
    app: app,
  }
  go h.Start()
  return h
}

func (h *MasterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  webs.Handler(h.handler).ServeHTTP(w, r)
}

func (h *MasterHandler) handler(ws *webs.Conn) {
  defer ws.Close()

  // Check to make sure this is master
  if master := h.app.Servant.FindNextMaster(); master.Name != h.app.ThisName() {
    sendActionError(ws, 0, ActionNewMaster, master.Addr)
    return
  }
  webs.JSON.Send(ws, Message{Action: ActionConnect})

  // Get the node
  node, err := h.recvNode(ws)
  if err != nil {
    return
  }
  // Send the message over and wait for the ready
  node.ready = make(chan bool)
  if !h.SendMsg(Message{Action: ActionConnect, node: node}) {
    // TODO
    return
  }
  ok := <-node.ready
  if !ok {
    return
  }
  // Read messages
  for {
    var msg Message
    // TODO: Wait
    if err := webs.JSON.Receive(ws, &msg); err != nil {
      break
    }
    // TODO: Should this be done in run?
    switch msg.Action {
    case ActionNewMaster:
      sendActionError(ws, msg.Id, ActionNewMaster, "cannot use action")
      continue
    case ActionHeartbeat:
      continue
    }
    msg.node = node
    if !h.SendMsg(msg) {
      // TODO
      return
    }
  }
  h.SendMsg(Message{
    Action: ActionDisconnect,
    node: node,
  })
}

func (h *MasterHandler) recvNode(ws *webs.Conn) (*Node, error) {
  var msg Message
  // Get the node
  if err := webs.JSON.Receive(ws, &msg); err != nil {
    return nil, err
  }
  if msg.Action != ActionConnect {
    // TODO: Send ActionConnect or ActionError?
    sendActionError(ws, msg.Id, ActionError, "expected action connect")
    return nil, fmt.Errorf("bad message")
  } else if pwd, _ := msg.Content.(string); pwd != h.app.MasterPassword {
    sendActionError(ws, msg.Id, ActionConnect, InvalidPassword)
    return nil, fmt.Errorf("incorrect password")
  }
  if len(msg.Nodes) != 1 {
    sendActionError(ws, msg.Id, ActionConnect, ExpectedNode)
    return nil, fmt.Errorf("bad message")
  }
  // Fill in the node
  node := msg.Nodes[0]
  if !node.IsStatic {
    node.Addr = "-"
  }
  node.ConnectedAt = time.Now().UnixNano()
  node.ws = ws
  return node, nil
}

func (h *MasterHandler) SendMsg(msg Message) bool {
  if r := h.runner.Load(); r != nil {
    return r.msgCh.Send(msg)
  }
  return false
}

// Returns false if it was already running (didn't start)
func (h *MasterHandler) Start() bool {
  runner := &masterRunner{
    handler: h,
    nodes: NodeArr{h.app.ThisNode.Clone()},
    msgCh: utils.NewUChan[Message](50),
  }
  if swapped := h.runner.CompareAndSwap(nil, runner); !swapped {
    return false
  }
  go runner.run()
  return true
}

// Returns false if it wasn't running (didn't stop)
func (h *MasterHandler) Stop() bool {
  runner := h.runner.Swap(nil)
  if runner == nil {
    return false
  }
  runner.stop()
  return true
}

func (h *MasterHandler) Broadcast(msg Message) {
  if r := h.runner.Load(); r != nil {
    r.Broadcast(msg)
  }
}

type masterRunner struct {
  handler *MasterHandler
  nodes NodeArr
  msgCh *utils.UChan[Message]
}

func (h *masterRunner) run() {
  timer := h.startTimer()
  for msg, ok := h.msgCh.Recv(); ok; msg, ok = h.msgCh.Recv() {
    switch msg.Action {
    case ActionConnect:
      h.handleConnect(msg)
    case ActionDisconnect:
      h.handleDisconnect(msg)
    case ActionMessage:
      h.handleMessage(msg)
    case ActionNewMaster:
      h.handleNewMaster(msg)
    case ActionHeartbeat:
      h.handleHeartbeat(msg)
    default:
      // TODO
    }
    if msg.node != nil && msg.node.ws != nil {
      webs.JSON.Send(msg.node.ws, Message{
        Id: msg.Id,
        Action: ActionResponse,
      })
    }
  }
  timer.Stop()
}

func (h *masterRunner) handleConnect(msg Message) {
  add := true
  // Check to make sure the node isn't a duplicate
  for _, node := range h.nodes {
    if node.Name == msg.node.Name {
      if node.Name == h.handler.app.ThisName() && node.ws == nil {
        // NOTE: This is here because when this node becomes master and the
        // servant needs to connect to the master (itself), there needed to be
        // a way to set the websocket.
        // The node doesn't need to be added since it's already in the list.
        node.ws = msg.node.ws
        add = false
        break
      } else {
        sendActionError(msg.node.ws, msg.Id, ActionConnect, NameExists)
        msg.node.setReady(false)
        return
      }
    }
    msg.node.IsMaster = false
  }
  if add {
    h.nodes = append(h.nodes, msg.node)
    bubbleNodes(h.nodes)
  }
  h.Broadcast(Message{
    Action: ActionConnect,
    //Nodes: h.nodes,
    Nodes: NodeArr{msg.node},
  })
  webs.JSON.Send(
    msg.node.ws,
    Message{
      Action: ActionHeartbeat,
      Nodes: h.nodes,
    },
  )
  msg.node.setReady(true)
}

func (h *masterRunner) handleDisconnect(msg Message) {
  found := false
  // Find the node to remove
  for i, node := range h.nodes {
    if node.Name == msg.node.Name {
      h.nodes = append(h.nodes[:i], h.nodes[i+1:]...)
      found = true
      break
    }
  }
  if !found {
    // TODO?
    return
  }
  h.Broadcast(Message{
    Action: ActionDisconnect,
    Content: msg.node.Name,
    Nodes: NodeArr{msg.node},
    //Nodes: h.nodes,
  })
}

func (h *masterRunner) handleMessage(msg Message) {
  h.Broadcast(Message{
    Action: ActionMessage,
    Content: msg.Content,
    Nodes: NodeArr{msg.node},
  })
}

func (h *masterRunner) handleNewHeartbeat(msg Message) {
  h.handler.Stop()
  h.Broadcast(Message{
    Action: ActionNewMaster,
    Content: msg.Content,
  })
}

func (h *masterRunner) handleHeartbeat(msg Message) {
  h.Broadcast(Message{
    Action: ActionHeartbeat,
    Nodes: h.nodes,
  })
}

func (h *masterRunner) startTimer() *time.Timer {
  var timer *time.Timer
  timer = time.AfterFunc(time.Second, func() {
    if !h.handler.app.IsMaster() {
    } else {
      if !h.msgCh.Send(Message{Action: ActionHeartbeat}) {
        return
      }
    }
    timer.Reset(time.Second)
  })
  return timer
}

func (h *masterRunner) stop() {
  h.msgCh.Close()
}

func (h *masterRunner) Broadcast(msg Message) {
  msgBytes, _ := json.Marshal(msg)
  for _, node := range h.nodes {
    if node.ws != nil {
      node.ws.Write(msgBytes)
    }
  }
}
