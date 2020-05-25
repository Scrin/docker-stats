package main

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"golang.org/x/sys/unix"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	containerPrefix = "container_"
	dataPrefix      = "container_data_"
)

var (
	knownContainerIDs      map[string]prometheus.Labels
	knownContainerNetworks map[string]prometheus.Labels
	knownDataNames         map[string]prometheus.Labels

	pids           *prometheus.GaugeVec
	cpuUsageUser   *prometheus.GaugeVec
	cpuUsageKernel *prometheus.GaugeVec
	cpuUsageTotal  *prometheus.GaugeVec
	memoryUsage    *prometheus.GaugeVec
	memoryLimit    *prometheus.GaugeVec

	networkReceiveBytes    *prometheus.GaugeVec
	networkTransmitBytes   *prometheus.GaugeVec
	networkReceivePackets  *prometheus.GaugeVec
	networkTransmitPackets *prometheus.GaugeVec
	networkReceiveErrors   *prometheus.GaugeVec
	networkTransmitErrors  *prometheus.GaugeVec
	networkReceiveDropped  *prometheus.GaugeVec
	networkTransmitDropped *prometheus.GaugeVec

	dataFree       *prometheus.GaugeVec
	dataAvailable  *prometheus.GaugeVec
	dataSize       *prometheus.GaugeVec
	dataInodesFree *prometheus.GaugeVec
	dataInodes     *prometheus.GaugeVec
)

func setup() {
	containerLabels := []string{"container_id", "container_name", "compose_project", "compose_service", "container_image_id", "container_image_name"}
	containerNetworkLabels := append(containerLabels, "interface")
	dataLabels := []string{"data_name"}

	pids = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "pids",
		Help: "Number of running processes in the container",
	}, containerLabels)
	cpuUsageUser = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "cpu_usage_user_seconds_total",
		Help: "Container CPU usage in user mode",
	}, containerLabels)
	cpuUsageKernel = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "cpu_usage_kernel_seconds_total",
		Help: "Container CPU usage in kernel mode",
	}, containerLabels)
	cpuUsageTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "cpu_usage_seconds_total",
		Help: "Container CPU usage",
	}, containerLabels)
	cpuUsageTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "cpu_usage_seconds_total",
		Help: "Container CPU usage",
	}, containerLabels)
	memoryUsage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "memory_usage_bytes",
		Help: "Container Memory usage",
	}, containerLabels)
	memoryLimit = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "memory_limit_bytes",
		Help: "Container Memory limit",
	}, containerLabels)

	networkReceiveBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "network_receive_bytes_total",
		Help: "Container network received bytes",
	}, containerNetworkLabels)
	networkTransmitBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "network_transmit_bytes_total",
		Help: "Container network transmitted bytes",
	}, containerNetworkLabels)
	networkReceivePackets = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "network_receive_packets_total",
		Help: "Container network received packets",
	}, containerNetworkLabels)
	networkTransmitPackets = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "network_transmit_packets_total",
		Help: "Container network transmitted packets",
	}, containerNetworkLabels)
	networkReceiveErrors = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "network_receive_errors_total",
		Help: "Container network receive errors",
	}, containerNetworkLabels)
	networkTransmitErrors = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "network_transmit_errors_total",
		Help: "Container network transmit errors",
	}, containerNetworkLabels)
	networkReceiveDropped = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "network_receive_dropped_total",
		Help: "Container network receive drops",
	}, containerNetworkLabels)
	networkTransmitDropped = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: containerPrefix + "network_transmit_dropped_total",
		Help: "Container network transmit drops",
	}, containerNetworkLabels)

	dataFree = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: dataPrefix + "free_bytes",
		Help: "Container data used bytes",
	}, dataLabels)
	dataAvailable = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: dataPrefix + "available_bytes",
		Help: "Container data available bytes",
	}, dataLabels)
	dataSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: dataPrefix + "size_bytes",
		Help: "Container data total bytes",
	}, dataLabels)
	dataInodesFree = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: dataPrefix + "free_inodes",
		Help: "Container data free inodes",
	}, dataLabels)
	dataInodes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: dataPrefix + "inodes",
		Help: "Container data total inodes",
	}, dataLabels)

	prometheus.MustRegister(pids)
	prometheus.MustRegister(cpuUsageUser)
	prometheus.MustRegister(cpuUsageKernel)
	prometheus.MustRegister(cpuUsageTotal)
	prometheus.MustRegister(memoryUsage)
	prometheus.MustRegister(memoryLimit)

	prometheus.MustRegister(networkReceiveBytes)
	prometheus.MustRegister(networkTransmitBytes)
	prometheus.MustRegister(networkReceivePackets)
	prometheus.MustRegister(networkTransmitPackets)
	prometheus.MustRegister(networkReceiveErrors)
	prometheus.MustRegister(networkTransmitErrors)
	prometheus.MustRegister(networkReceiveDropped)
	prometheus.MustRegister(networkTransmitDropped)

	prometheus.MustRegister(dataFree)
	prometheus.MustRegister(dataAvailable)
	prometheus.MustRegister(dataSize)
	prometheus.MustRegister(dataInodesFree)
	prometheus.MustRegister(dataInodes)
}

func updateContainers(docker *client.Client) {
	newKnownContainerIDs := make(map[string]prometheus.Labels)
	newKnownContainerNetworks := make(map[string]prometheus.Labels)
	containers, err := docker.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		log.Print("Failed to get container list: ", err)
	}
	for _, container := range containers {
		resp, err := docker.ContainerStatsOneShot(context.Background(), container.ID)
		if err != nil {
			log.Print("Failed to get container stats: ", err)
			continue
		}
		stats := types.StatsJSON{}
		err = json.NewDecoder(resp.Body).Decode(&stats)
		if err != nil {
			log.Print("Failed to parse container stats: ", err)
			continue
		}
		labels := prometheus.Labels{
			"container_id":         container.ID,
			"container_name":       strings.TrimPrefix(container.Names[0], "/"),
			"compose_project":      container.Labels["com.docker.compose.project"],
			"compose_service":      container.Labels["com.docker.compose.service"],
			"container_image_id":   strings.TrimPrefix(container.ImageID, "sha256:"),
			"container_image_name": container.Image,
		}
		resp.Body.Close()
		newKnownContainerIDs[container.ID] = labels

		pids.With(labels).Set(float64(stats.PidsStats.Current))
		cpuUsageUser.With(labels).Set(float64(stats.CPUStats.CPUUsage.UsageInUsermode) / 1e9)
		cpuUsageKernel.With(labels).Set(float64(stats.CPUStats.CPUUsage.UsageInKernelmode) / 1e9)
		cpuUsageTotal.With(labels).Set(float64(stats.CPUStats.CPUUsage.TotalUsage) / 1e9)
		memoryUsage.With(labels).Set(float64(stats.MemoryStats.Usage - stats.MemoryStats.Stats["cache"]))
		memoryLimit.With(labels).Set(float64(stats.MemoryStats.Limit))

		for intf, net := range stats.Networks {
			labels := prometheus.Labels{
				"container_id":         container.ID,
				"container_name":       strings.TrimPrefix(container.Names[0], "/"),
				"compose_project":      container.Labels["com.docker.compose.project"],
				"compose_service":      container.Labels["com.docker.compose.service"],
				"container_image_id":   strings.TrimPrefix(container.ImageID, "sha256:"),
				"container_image_name": container.Image,
				"interface":            intf,
			}
			newKnownContainerNetworks[container.ID+intf] = labels
			networkReceiveBytes.With(labels).Set(float64(net.RxBytes))
			networkTransmitBytes.With(labels).Set(float64(net.TxBytes))
			networkReceivePackets.With(labels).Set(float64(net.RxPackets))
			networkTransmitPackets.With(labels).Set(float64(net.TxPackets))
			networkReceiveErrors.With(labels).Set(float64(net.RxErrors))
			networkTransmitErrors.With(labels).Set(float64(net.TxErrors))
			networkReceiveDropped.With(labels).Set(float64(net.RxDropped))
			networkTransmitDropped.With(labels).Set(float64(net.TxDropped))
		}
	}
	for id, labels := range knownContainerIDs {
		if newKnownContainerIDs[id] == nil {
			pids.Delete(labels)
			cpuUsageUser.Delete(labels)
			cpuUsageKernel.Delete(labels)
			cpuUsageTotal.Delete(labels)
			memoryUsage.Delete(labels)
			memoryLimit.Delete(labels)
		}
	}
	for id, labels := range knownContainerNetworks {
		if newKnownContainerNetworks[id] == nil {
			networkReceiveBytes.Delete(labels)
			networkTransmitBytes.Delete(labels)
			networkReceivePackets.Delete(labels)
			networkTransmitPackets.Delete(labels)
			networkReceiveErrors.Delete(labels)
			networkTransmitErrors.Delete(labels)
			networkReceiveDropped.Delete(labels)
			networkTransmitDropped.Delete(labels)
		}
	}
	knownContainerIDs = newKnownContainerIDs
	knownContainerNetworks = newKnownContainerNetworks
}

func updateDatas(basepath string) {
	newKnownDataNames := make(map[string]prometheus.Labels)
	var paths []string
	if basepath == "/" {
		paths = append(paths, "/")
	} else {
		files, err := ioutil.ReadDir(basepath)
		if err != nil {
			log.Print("Failed to get data names: ", err)
			return
		}
		for _, f := range files {
			if f.IsDir() {
				paths = append(paths, f.Name())
			}
		}
	}

	for _, path := range paths {
		fs := unix.Statfs_t{}
		err := unix.Statfs(basepath+"/"+path, &fs)
		if err != nil {
			log.Print("Failed to stat "+basepath+"/"+path+": ", err)
			continue
		}
		labels := prometheus.Labels{"data_name": path}

		dataFree.With(labels).Set(float64(fs.Bfree * uint64(fs.Bsize)))
		dataAvailable.With(labels).Set(float64(fs.Bavail * uint64(fs.Bsize)))
		dataSize.With(labels).Set(float64(fs.Blocks * uint64(fs.Bsize)))
		dataInodesFree.With(labels).Set(float64(fs.Ffree))
		dataInodes.With(labels).Set(float64(fs.Files))
	}
	for id, labels := range knownDataNames {
		if newKnownDataNames[id] == nil {
			dataFree.Delete(labels)
			dataAvailable.Delete(labels)
			dataSize.Delete(labels)
			dataInodesFree.Delete(labels)
			dataInodes.Delete(labels)
		}
	}
	knownDataNames = newKnownDataNames
}

func updateMetrics(docker *client.Client, basepath string) {
	for {
		updateContainers(docker)
		updateDatas(basepath)
	}
}

func main() {
	basepath := "/"
	if len(os.Args) > 1 {
		_, err := ioutil.ReadDir(os.Args[1])
		if err != nil {
			log.Fatal(err)
		}
		basepath = os.Args[1]
	}

	docker, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	setup()
	go updateMetrics(docker, basepath)

	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(":8080", nil)
}
