package cmd

import (
	"fmt"
	"log"
	"net"

	"os"
	"time"

	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
	"github.com/spf13/cobra"
	dbSqlite "github.com/vu-ngoc-son/XDP-p2p-router/database/db-sqlite"
	"github.com/vu-ngoc-son/XDP-p2p-router/database/geolite2"
	bpfLoader "github.com/vu-ngoc-son/XDP-p2p-router/internal/bpf-loader"
	"github.com/vu-ngoc-son/XDP-p2p-router/internal/common"
	"github.com/vu-ngoc-son/XDP-p2p-router/internal/compute"
	"github.com/vu-ngoc-son/XDP-p2p-router/internal/ip2location"
	limitBand "github.com/vu-ngoc-son/XDP-p2p-router/internal/limit-band"
	myWidget "github.com/vu-ngoc-son/XDP-p2p-router/internal/monitor/widgets"
	packetCapture "github.com/vu-ngoc-son/XDP-p2p-router/internal/packet-capture"
)

var (
	device string

	stderrLogger = log.New(os.Stderr, "", 0)

	hostPublicIP  string
	hostPrivateIP net.IP

	geoDB    *geolite2.GeoLite2
	sqliteDB *dbSqlite.SQLiteDB

	// TODO: this should be configurable
	fakeData       bool
	updateInterval = time.Second

	grid           *ui.Grid
	ipStats        *myWidget.IPStats
	peerStatsPie   *myWidget.PeersPie
	peerStatsTable *myWidget.PeersTable
	whiteList      *myWidget.WhiteList
	basicInfo      *widgets.Paragraph
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the router",
	Run:   execStartCmd,
}

func init() {
	// TODO: these vars should be configurable
	asnDBPath := fmt.Sprintf("/home/ted/TheFirstProject/XDP-p2p-router/data/geolite2/GeoLite2-ASN_%s/GeoLite2-ASN.mmdb", "20210504")
	cityDBPath := fmt.Sprintf("/home/ted/TheFirstProject/XDP-p2p-router/data/geolite2/GeoLite2-City_%s/GeoLite2-City.mmdb", "20210427")
	countryDBPath := fmt.Sprintf("/home/ted/TheFirstProject/XDP-p2p-router/data/geolite2/GeoLite2-Country_%s/GeoLite2-Country.mmdb", "20210427")
	sqliteDBPath := "/home/ted/TheFirstProject/XDP-p2p-router/data/sqlite/p2p-router.db"

	rootCmd.AddCommand(startCmd)

	startCmd.Flags().StringVar(&device, "device", "wlp8s0", "network interface that you want to attach this program to it")

	var err error

	hostPublicIP, err = common.GetMyPublicIP()
	if err != nil {
		stderrLogger.Fatalln("failed to get host public ip: ", err)
	}

	hostPrivateIP, err = common.GetMyPrivateIP(device)
	if err != nil {
		stderrLogger.Fatalln("failed to get host private ip: ", err)
	}

	geoDB = geolite2.NewGeoLite2(asnDBPath, cityDBPath, countryDBPath, hostPublicIP)

	sqliteDB, err = dbSqlite.NewSQLite(sqliteDBPath)
	if err != nil {
		stderrLogger.Fatalln("failed to connect to sqlite", err)
		return
	}
}

func execStartCmd(_ *cobra.Command, _ []string) {
	hostInfo, err := geoDB.HostInfo()
	if err != nil {
		stderrLogger.Fatalln("failed to query host info", err)
		return
	}

	err = sqliteDB.CreateHost(hostInfo)
	if err != nil {
		stderrLogger.Fatalln("failed to add host info to database", err)
		return
	}

	m := bpfLoader.LoadModule(hostPrivateIP)
	pktCapture, err := packetCapture.Start(device, m)
	if err != nil {
		stderrLogger.Fatalln("failed to start packet capture module", err)
	}
	defer packetCapture.Close(device, m)
	limiter, err := limitBand.NewLimiter(m)
	if err != nil {
		stderrLogger.Fatalln("failed to init limiter module")
	}
	defer limitBand.Close(device, m)

	locator := ip2location.NewLocator(pktCapture, sqliteDB, geoDB)
	calculator := compute.NewCalculator(sqliteDB)
	//watchDog := monitor.NewMonitor(pktCapture, limiter, sqliteDB)

	stderrLogger.Println("starting router ... Ctrl+C to stop.")

	go func() {
		for {
			time.Sleep(5 * time.Second)
			locator.UpdatePeersToDB()
		}
	}()

	go func() {
		for {
			time.Sleep(15 * time.Second)
			err := calculator.UpdatePeersLimit()
			if err != nil {
				stderrLogger.Println("calculator | failed to update peer limit ", err)
			}
		}
	}()
	go func() {
		for {
			time.Sleep(15 * time.Second)
			err := calculator.UpdatePeersLimit()
			if err != nil {
				stderrLogger.Println("calculator | failed to update peer limit ", err)
			}
		}
	}()
	go func() {
		for {
			time.Sleep(5 * time.Second)
			_, err := limiter.ExportMap()
			if err != nil {
				stderrLogger.Println("limiter | failed to export map ", err)
			}
		}
	}()

	if err := ui.Init(); err != nil {
		stderrLogger.Fatalln("failed to initialize termui: %v\n", err)
	}
	defer ui.Close()

	fakeData = true
	initWidgets(fakeData)

	setupGrid()
	termWidth, termHeight := ui.TerminalDimensions()
	fmt.Println(termHeight, termWidth)
	grid.SetRect(0, 0, termWidth, termHeight)
	ui.Render(grid)

	eventLoop()
}

func setupGrid() {
	grid = ui.NewGrid()

	grid.Set(
		ui.NewRow(1.0/2,
			ui.NewCol(1.0/2, ipStats),
			ui.NewCol(1.0/4, peerStatsPie),
			ui.NewCol(1.0/4, peerStatsTable),
		),
		ui.NewRow(1.0/2,
			ui.NewCol(1.0/2, basicInfo),
			ui.NewCol(1.0/2, whiteList),
		),
	)
}

func initWidgets(fakeData bool) {
	ipStats = myWidget.NewIPStats(updateInterval, sqliteDB, fakeData)
	peerStatsPie = myWidget.NewPeersPie(updateInterval, sqliteDB, fakeData)
	peerStatsTable = myWidget.NewPeersTable(updateInterval, sqliteDB, fakeData)
	whiteList = myWidget.NewWhiteList(updateInterval, sqliteDB, fakeData)
	basicInfo = widgets.NewParagraph()
	basicInfo.Text = fmt.Sprintf(
		"Public IP: %s\nPrivate IP: %s\n",
		hostPublicIP, hostPrivateIP.String(),
	)

}

func eventLoop() {
	drawTicker := time.NewTicker(updateInterval).C
	uiEvents := ui.PollEvents()

	for {
		select {
		case e := <-uiEvents:
			switch e.ID {
			case "q", "<C-c>":
				return
			}
		case <-drawTicker:
			termWidth, termHeight := ui.TerminalDimensions()
			grid.SetRect(0, 0, termWidth, termHeight)
			ui.Render(grid)
		}
	}

}
