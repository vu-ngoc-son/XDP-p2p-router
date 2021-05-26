package cmd

import (
	"fmt"
	"github.com/vu-ngoc-son/XDP-p2p-router/internal/ip2location"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	dbSqlite "github.com/vu-ngoc-son/XDP-p2p-router/database/db-sqlite"
	"github.com/vu-ngoc-son/XDP-p2p-router/database/geolite2"
	bpfLoader "github.com/vu-ngoc-son/XDP-p2p-router/internal/bpf-loader"
	packetCapture "github.com/vu-ngoc-son/XDP-p2p-router/internal/packet-capture"
)

var (
	device string
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the router",
	Run:   execStartCmd,
}

func init() {
	rootCmd.AddCommand(startCmd)

	startCmd.Flags().StringVar(&device, "device", "wlp8s0", "network interface that you want to attach this program to it")
}

func execStartCmd(startCmd *cobra.Command, args []string) {
	asnDBPath := "/home/ted/TheFirstProject/XDP-p2p-router/data/geolite2/GeoLite2-ASN_20210504/GeoLite2-ASN.mmdb"
	cityDBPath := "/home/ted/TheFirstProject/XDP-p2p-router/data/geolite2/GeoLite2-City_20210427/GeoLite2-City.mmdb"
	countryDBPath := "/home/ted/TheFirstProject/XDP-p2p-router/data/geolite2/GeoLite2-Country_20210427/GeoLite2-Country.mmdb"
	sqliteDBPath := "/home/ted/TheFirstProject/XDP-p2p-router/data/sqlite/p2p-router.db"

	geoDB := geolite2.NewGeoLite2(asnDBPath, cityDBPath, countryDBPath)

	sqliteDB, err := dbSqlite.NewSQLite(sqliteDBPath)
	if err != nil {
		fmt.Println("failed to connect to sqlite", err)
		return
	}

	m := bpfLoader.LoadModule(device)
	p, err := packetCapture.Start(device, m)
	if err != nil {
		fmt.Println("failed to start packet capture module")
		os.Exit(1)
	}
	defer packetCapture.Close(device, m)

	locator := ip2location.NewLocator(p, sqliteDB, geoDB)

	fmt.Println("starting router ... Ctrl+C to stop.")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan bool)
	go func() {
		sig := <-signals
		fmt.Printf("\n%v\n", sig)
		done <- true
	}()

	go func() {
		for {
			time.Sleep(5 * time.Second)
			locator.UpdatePeersToDB()
		}
	}()

	_ = <-done
	fmt.Println("shutting down gracefully ...")
}
