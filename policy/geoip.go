package policy

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/oschwald/maxminddb-golang"
)

// GeoDB is a MaxMind country database (GeoLite2-Country.mmdb or similar).
type GeoDB struct {
	r *maxminddb.Reader
}

// OpenGeoDB opens an mmdb file. A nil *GeoDB is safe to use (Country returns "").
func OpenGeoDB(path string) (*GeoDB, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("policy: open geoip %s: %w", path, err)
	}
	return &GeoDB{r: r}, nil
}

type geoRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// Country returns the uppercase ISO country code for addr, or "" when unknown
// or when the database is unavailable.
func (g *GeoDB) Country(addr netip.Addr) string {
	if g == nil || g.r == nil || !addr.IsValid() {
		return ""
	}
	ip := net.IP(addr.Unmap().AsSlice())
	var rec geoRecord
	if err := g.r.Lookup(ip, &rec); err != nil {
		return ""
	}
	return rec.Country.ISOCode
}

// Close releases the database.
func (g *GeoDB) Close() error {
	if g == nil || g.r == nil {
		return nil
	}
	return g.r.Close()
}
