package fastlike

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net"
	"net/http"
)

// Geo represents geographic data associated with a particular IP address
// See: https://docs.rs/crate/fastly/0.3.2/source/src/geo.rs
type Geo struct {
	ASName           string  `json:"as_name"`
	ASNumber         int     `json:"as_number"`
	AreaCode         int     `json:"area_code"`
	City             string  `json:"city"`
	ConnSpeed        string  `json:"conn_speed"`
	ConnType         string  `json:"conn_type"`
	Continent        string  `json:"continent"`
	CountryCode      string  `json:"country_code"`
	CountryCode3     string  `json:"country_code3"`
	CountryName      string  `json:"country_name"`
	Latitude         float64 `json:"latitude"`
	Longitude        float64 `json:"longitude"`
	MetroCode        int     `json:"metro_code"`
	PostalCode       string  `json:"postal_code"`
	ProxyDescription string  `json:"proxy_description"`
	ProxyType        string  `json:"proxy_type"`
	Region           string  `json:"region,omitempty"`
	UTCOffset        int     `json:"utc_offset"`
}

func DefaultGeo(ip net.IP) Geo {
	return Geo{
		ASName:   "fastlike",
		ASNumber: 64496,

		AreaCode:     512,
		City:         "Austin",
		CountryCode:  "US",
		CountryCode3: "USA",
		CountryName:  "United States of America",
		Continent:    "NA",
		Region:       "TX",

		ConnSpeed: "satellite",
		ConnType:  "satellite",
	}
}

func GeoHandler(fn func(net.IP) Geo) Backend {
	return func(r *http.Request) (*http.Response, error) {
		var ip = net.ParseIP(r.Header.Get("fastly-xqd-arg1"))
		var geo = fn(ip)

		var buf = new(bytes.Buffer)
		json.NewEncoder(buf).Encode(geo)

		var w = &http.Response{
			Status:     http.StatusText(http.StatusOK),
			StatusCode: http.StatusOK,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Body:       ioutil.NopCloser(buf),
			Header:     make(http.Header, 0),
		}

		return w, nil
	}

}
