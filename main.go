package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"
)

var (
	apiKey     string
	zoneID     string
	recordName string
	refresh    time.Duration
	resolver   string
	hostname   string
)

const (
	baseURL = "https://dns.api.gandi.net/api/v5/zones"
)

// Define and parse flags
func init() {
	flag.StringVar(&apiKey, "apikey", "", "Mandatory. API key to access server platform")
	flag.StringVar(&zoneID, "zone", "", "Mandatory. Zone uuid")
	flag.StringVar(&recordName, "record", "", "Mandatory. Record to update")
	flag.DurationVar(&refresh, "refresh", 5*time.Minute, "Delay between checks for public IP address updates")
	flag.StringVar(&resolver, "resolver", "resolver1.opendns.com", "The resolver to check use for `myip` record")
	flag.StringVar(&hostname, "myip", "myip.opendns.com", "The hostname of the record to use to check for current IP")
}

type publicIPResolver struct {
	Hostname string
	Server   string
}

// Resolve gets the current pblic IP
func (p *publicIPResolver) Resolve() (string, error) {
	output, err := exec.Command("dig", "+time=1", "+short", p.Hostname, "@"+p.Server).Output()
	if err != nil {
		return "", err
	}
	if len(output) == 0 {
		//fail.
		return "", errors.New("no ipv4 valid address")
	}
	stringIP := string(output[0 : len(output)-1]) //output has a trailing newline
	ip := net.ParseIP(stringIP)
	if ip == nil || ip.To4() == nil {
		return "", errors.New("no ipv4 valid address")
	}
	return stringIP, nil
}

type liveDNSRecord struct {
	Kind   string   `json:"rrset_type,omitempty"`
	Name   string   `json:"rrset_name,omitempty"`
	TTL    uint     `json:"rrset_ttl,omitempty"`
	Values []string `json:"rrset_values,omitempty"`
}

type liveDNSConfig struct {
	Key    string
	Zone   string
	Record string
}

func (l *liveDNSConfig) req(method string, body io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("%s/%s/records/%s/A", baseURL, l.Zone, l.Record)
	//log.Println(url)
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Api-Key", l.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	return http.DefaultClient.Do(req)
}

// Get gets the Current value of the record
func (l *liveDNSConfig) Get() (string, error) {
	res, err := l.req("GET", nil)
	if err != nil {
		return "", err
	}
	record := &liveDNSRecord{}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(record); err != nil {
		return "", err
	}
	if record.Values == nil || len(record.Values) == 0 || record.Values[0] == "" {
		//log.Println(record)
		return "", errors.New("Invalid Record Response")
	}
	return record.Values[0], nil
}

// Set sets the value of the Record
func (l *liveDNSConfig) Set(ip string) error {
	body := &bytes.Buffer{}
	err := json.NewEncoder(body).Encode(&liveDNSRecord{TTL: 300, Values: []string{ip}})
	if err != nil {
		return err
	}
	res, err := l.req("PUT", body)
	if err != nil {
		return err
	}

	// we should get a created code
	if res.StatusCode != http.StatusCreated {
		return fmt.Errorf("Unexpected Response Status Code [%d]", res.StatusCode)
	}
	return nil
}

func main() {
	flag.Parse()
	if apiKey == "" || recordName == "" || zoneID == "" {
		fmt.Println("Missing one or more command line options.")
		flag.PrintDefaults()
		os.Exit(2)
	}

	dyndns := &liveDNSConfig{
		Key:    apiKey,
		Zone:   zoneID,
		Record: recordName,
	}

	publicip := &publicIPResolver{
		Hostname: hostname,
		Server:   resolver,
	}

	var registeredIP, currentIP string
	var err error

	loop := func() {
		// Get the current public address
		currentIP, err = publicip.Resolve()
		if err != nil {
			log.Println("Error: failed to get pulic IP:", err)
			return
		}

		if registeredIP == "" {
			registeredIP, err = dyndns.Get()
			if err != nil {
				log.Println("Error: failed to to get current dyndns record:", err)
				return
			}

			if registeredIP != currentIP {
				if err = dyndns.Set(currentIP); err != nil {
					log.Println("Error: updating DNS record:", err)
					return
				}
				log.Print("Info: updated Gandi records with IP:", currentIP)
			}
		}
	}

	for {
		loop()
		time.Sleep(refresh)
	}
}
