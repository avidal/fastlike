package fastlike

import (
	"encoding/json"
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

func defaultGeoLookup(ip net.IP) Geo {
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

func geoHandler(fn func(ip net.IP) Geo) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addr := net.ParseIP(r.Header.Get("fastly-xqd-arg1"))
		geo := fn(addr)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(geo)
	})
}
