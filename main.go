package main

import (
  "log"
  "net/http"
  "os"
  "time"

  utils "github.com/johnietre/utils/go"
  "github.com/spf13/cobra"
)

func main() {
  rootCmd := cobra.Command{
    Use: "nodes <ADDR>",
    Args: cobra.ExactArgs(1),
    Run: Run,
  }
  flags := rootCmd.Flags()
  flags.Bool("static", false, "Whether the addr is static (node can be master)")
  flags.String("master", "", "Address of master to connect to")
  flags.String("name", "", "Name of the node")
  flags.String(
    "servant-password", "",
    "Password used by servants (e.g., the web)",
  )
  flags.String(
    "master-password", "",
    "Password used to connect to master (other masters and this)",
  )
  rootCmd.MarkFlagRequired("name")

  if err := rootCmd.Execute(); err != nil {
    os.Exit(1)
  }
}

func Run(cmd *cobra.Command, args []string) {
  addr := args[0]
  flags := cmd.Flags()
  name, _ := flags.GetString("name")
  if name == "" {
    log.Fatal("name must not be empty")
  }
  static, _ := flags.GetBool("static")
  masterAddr, _ := flags.GetString("master")
  if masterAddr == "" {
    masterAddr = addr
  }
  masterPwd, _ := flags.GetString("master-password")
  var servantPwd *string
  if f := cmd.Flag("servant-password"); f.Changed {
    servantPwd = utils.NewT(f.Value.String())
  }

  app := NewHandler(&HandlerConfig{
    Addr: addr,
    Name: name,
    Static: static,
    ServantPassword: servantPwd,
    MasterPassword: masterPwd,
  })
  srvr := &http.Server{
    Addr: addr,
    Handler: app,
  }
  done := make(chan utils.Unit)
  go func() {
    log.Fatal("Error serving: ", srvr.ListenAndServe())
    close(done)
  }()
  time.Sleep(time.Second)
  if err := app.Servant.ConnectMaster(masterAddr, false); err != nil {
    log.Fatal("Error connecting to master: ", err)
  }
  log.Print("Running on ", addr)
  <-done
}
