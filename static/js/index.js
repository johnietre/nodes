class Action {
  static Connect = "connect";
  static Disconnect = "disconnect";
  static Message = "message";
  static Heartbeat = "heartbeat";
  static NewMaster = "new-master";
  static Error = "error";
};

function newMessage() {
  return {
    from: "",
    content: "",
  };
}

const App = {
  data() {
    const url = new URL("/ws", document.location.href);
    url.protocol = (url.protocol == "https") ? "wss" : "ws";
    const ws = new WebSocket(url.toString());
    ws.onopen = this.openHandler;
    ws.onmessage = this.msgHandler;
    ws.onerror = this.errHandler;
    ws.onclose = this.closeHandler;
    return {
      //name: "{|.Name|}",
      //name: document.title.substr(7),
      name: "",

      isMaster: false,
      connected: false,
      masterAddr: "",
      nodes: [],

      msgContent: "",
      messages: [],

      ws: ws,
      receivedInitial: false,
      _blank: undefined
    };
  },

  methods: {
    sendMsg() {
      const err = this.send({action: Action.Message, content: this.msgContent});
      if (err) {
        alert(`Error sending message: ${e}`);
      } else {
        this.msgContent = "";
      }
    },
    send(msg) {
      try {
        this.ws.send(JSON.stringify(msg));
      } catch (e) {
        console.log(`error sending message: ${e}`);
        return e;
      }
    },
    connect() {
      let url;
      try {
        url = new URL(this.masterAddr);
      } catch (e) {
        alert(`Invalid URL ${e}`);
        return;
      }
      const err = this.send({action: Action.NewMaster, content: url.toString()});
      if (err) {
        alert(`Error sending connect: ${e}`);
      }
    },
    disconnect() {
      const err = this.send({action: Action.Disconnect});
      if (err) {
        alert(`Error sending disconnect: ${e}`);
      }
    },
    checkMaster() {
      this.isMaster = false;
      for (const node of this.nodes) {
        if (node.name === this.name) {
          this.isMaster = node.isMaster || false;
          break;
        } else if (node.isMaster) {
          this.masterAddr = node.addr;
          this.connected = true;
        }
      }
      if (this.isMaster) {
        this.masterAddr = "";
        this.connected = false;
      }
    },

    openHandler() {},
    msgHandler(ev) {
      let msg;
      try {
        msg = JSON.parse(ev.data);
      } catch (e) {
        console.log(`error parsing message: ${e}`);
        console.log(`bad message: ${ev.data}`);
      }
      if (!this.receivedInitial &&
        msg.action != Action.Connect &&
        msg.action != Action.Error) {
        // TODO?
        return;
      }
      switch (msg.action) {
        case Action.Connect:
          this.handleConnect(msg);
          break;
        case Action.Disconnect:
          this.handleDisconnect(msg);
          break;
        case Action.Message:
          this.handleMessage(msg);
          break;
        case Action.Heartbeat:
          this.handleHeartbeat(msg);
          break;
        case Action.Error:
          this.handleError(msg);
          break;
        default:
      }
    },
    errHandler(ev) {
      console.log(`websocket error: ${ev}`);
    },
    closeHandler(ev) {
      alert("Connection closed");
      console.log(`websocket closed: Code: ${ev.code}, Reason: ${ev.reason}`);
    },

    handleConnect(msg) {
      if (!this.receivedInitial) {
        this.handleInitialConnect(msg);
        return;
      }
      const node = msg.nodes[0];
      if (this.getNodeIndex(node.name) != -1) {
        return;
      }
      this.nodes.push(node);
      this.checkMaster();
      this.messages.push({
        from: "SYSTEM",
        content: `CONNECTED: ${node.name} (${node.addr})`
      });
    },
    handleInitialConnect(msg) {
      if (typeof msg.content === "string") {
        this.receivedInitial = true;
        this.name = msg.content;
        document.title = `Nodes: ${this.name}`;
        return;
      }
      if (msg.error === "invalid password") {
        alert("Invalid password");
        msg.error = "password required";
      }
      if (msg.error === "password required") {
        const pwd = prompt("Password");
        this.send({
          action: Action.Connect,
          content: pwd
        });
        return;
      }
      console.log(`Received unexpected connect error: ${msg.error}`);
      alert(`Connect error: ${msg.error}`);
    },
    handleDisconnect(msg) {
      const name = msg.content;
      const index = this.getNodeIndex(name);
      if (index != -1) {
        this.nodes.splice(index, 1);
      }
      const node = msg.nodes[0];
      this.messages.push({
        from: "SYSTEM",
        content: `DISCONNECTED: ${node.name} (${node.addr})`
      });
      //this.checkMaster();
    },
    handleMessage(msg) {
      const node = msg.nodes[0];
      this.messages.push({
        from: `${node.name} (${node.addr})`,
        content: msg.content
      });
    },
    handleHeartbeat(msg) {
      this.nodes = msg.nodes;
      this.checkMaster();
    },
    handleError(msg) {
      console.log(`received error: ${msg.error}`);
          alert(`Error: ${msg.error}`);
    },

    getNodeIndex(name) {
      for (let i = 0; i < this.nodes.length; i++) {
        if (this.nodes[i].name === name) {
          return i;
        }
        return -1;
      }
    },
    getNode(name) {
      const index = this.getNodeIndex(name);
      return (index == -1) ? null : this.nodes[index];
    },

    _blankMethod() {}
  }
};
const app = Vue.createApp(App);
app.mount("#app");
