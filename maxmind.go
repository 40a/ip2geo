package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (maxmind *MaxMind) Generate() {
	answer, err := maxmind.Download()
	if err != nil {
		maxmind.ErrorsChan <- Error{err, "MaxMind", "Download"}
		return
	}
	printMessage("MaxMind", "Download", "OK")
	err = maxmind.Unpack(answer)
	if err != nil {
		maxmind.ErrorsChan <- Error{err, "MaxMind", "Unpack"}
		return
	}
	printMessage("MaxMind", "Unpack", "OK")
	cities, err := maxmind.GenerateCities()
	if err != nil {
		maxmind.ErrorsChan <- Error{err, "MaxMind", "Generate Cities"}
		return
	}
	printMessage("MaxMind", "Generate cities", "OK")
	err = maxmind.GenerateNetwork(cities)
	if err != nil {
		maxmind.ErrorsChan <- Error{err, "MaxMind", "Generate db"}
		return
	}
	printMessage("MaxMind", "Generate db", "OK")
	if err := maxmind.WriteMap(); err != nil {
		maxmind.ErrorsChan <- Error{err, "MaxMind", "Write nginx maps"}
		return
	}
	printMessage("MaxMind", "Write nginx maps", "OK")
	maxmind.ErrorsChan <- Error{err: nil}
}

func (maxmind *MaxMind) Download() ([]byte, error) {
	resp, err := http.Get("http://geolite.maxmind.com/download/geoip/database/GeoLite2-City-CSV.zip")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	answer, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return answer, nil
}

func (maxmind *MaxMind) Unpack(response []byte) error {
	zipReader, err := zip.NewReader(bytes.NewReader(response), int64(len(response)))
	if err != nil {
		return err
	}
	maxmind.archive = zipReader.File
	return nil
}

func (maxmind *MaxMind) GenerateCities() (map[string]Location, error) {
	locations := make(map[string]Location)
	currentTime := time.Now()
	filename := "GeoLite2-City-Locations-" + maxmind.lang + ".csv"
	for record := range readCSVDatabase(maxmind.archive, filename, "MaxMind", ',', false) {
		if len(record) < 13 {
			printMessage("MaxMind", fmt.Sprintf(filename+" too short line: %s", record), "FAIL")
			continue
		}
		country := record[4]
		if len(record[10]) < 1 || len(country) < 1 {
			continue
		}
		if len(maxmind.include) < 1 || strings.Contains(maxmind.include, country) {
			if !strings.Contains(maxmind.exclude, country) {
				tz := record[12]
				if !maxmind.tzNames {
					tz = convertTZToOffset(currentTime, record[12])
				}
				locations[record[0]] = Location{
					ID:   record[0],
					City: record[10],
					TZ:   tz,
				}
			}
		}
	}
	if len(locations) < 1 {
		return nil, errors.New("Locations db is empty")
	}
	return locations, nil
}

func (maxmind *MaxMind) GenerateNetwork(locations map[string]Location) error {
	var database Database
	filename := "GeoLite2-City-Blocks-IPv" + strconv.Itoa(maxmind.ipver) + ".csv"
	for record := range readCSVDatabase(maxmind.archive, filename, "MaxMind", ',', false) {
		if len(record) < 2 {
			printMessage("MaxMind", fmt.Sprintf(filename+" too short line: %s", record), "FAIL")
			continue
		}
		ipRange := getIPRange(maxmind.ipver, record[0])
		netIP := net.ParseIP(strings.Split(ipRange, "-")[0])
		if netIP == nil {
			continue
		}
		geoID := record[1]
		if location, ok := locations[geoID]; ok {
			database = append(database, Location{
				ID:      geoID,
				City:    location.City,
				Network: ipRange,
				TZ:      location.TZ,
				NetIP:   ip2Int(netIP),
			})
		}
	}
	if len(database) < 1 {
		return errors.New("Network db is empty")
	}
	sort.Sort(database)
	maxmind.database = database
	return nil
}

func (maxmind *MaxMind) WriteMap() error {
	city, err := openMapFile(maxmind.OutputDir, "mm_city.txt")
	if err != nil {
		return err
	}
	tz, err := openMapFile(maxmind.OutputDir, "mm_tz.txt")
	if err != nil {
		return err
	}
	defer city.Close()
	defer tz.Close()
	for _, location := range maxmind.database {
		fmt.Fprintf(city, "%s %s;\n", location.Network, base64.StdEncoding.EncodeToString([]byte(location.City)))
		fmt.Fprintf(tz, "%s %s;\n", location.Network, location.TZ)
	}
	return nil
}
