package acl

import (
	"bufio"
	lru "github.com/hashicorp/golang-lru"
	"github.com/oschwald/geoip2-golang"
	"github.com/tobyxdd/hysteria/pkg/utils"
	"net"
	"os"
	"strings"
)

const entryCacheSize = 1024

type Engine struct {
	DefaultAction Action
	Entries       []Entry
	Cache         *lru.ARCCache
	ResolveIPAddr func(string) (*net.IPAddr, error)
	GeoIPReader   *geoip2.Reader
}

type cacheEntry struct {
	Action Action
	Arg    string
}

func LoadFromFile(filename string, resolveIPAddr func(string) (*net.IPAddr, error), geoIPLoadFunc func() (*geoip2.Reader, error)) (*Engine, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	entries := make([]Entry, 0, 1024)
	var geoIPReader *geoip2.Reader
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) == 0 || strings.HasPrefix(line, "#") {
			// Ignore empty lines & comments
			continue
		}
		entry, err := ParseEntry(line)
		if err != nil {
			return nil, err
		}
		if len(entry.Country) > 0 && geoIPReader == nil {
			geoIPReader, err = geoIPLoadFunc() // lazy load GeoIP reader only when needed
			if err != nil {
				return nil, err
			}
		}
		entries = append(entries, entry)
	}
	cache, err := lru.NewARC(entryCacheSize)
	if err != nil {
		return nil, err
	}
	return &Engine{
		DefaultAction: ActionProxy,
		Entries:       entries,
		Cache:         cache,
		ResolveIPAddr: resolveIPAddr,
		GeoIPReader:   geoIPReader,
	}, nil
}

// action, arg, isDomain, resolvedIP, error
func (e *Engine) ResolveAndMatch(host string) (Action, string, bool, *net.IPAddr, error) {
	ip, zone := utils.ParseIPZone(host)
	if ip == nil {
		// Domain
		ipAddr, err := e.ResolveIPAddr(host)
		if v, ok := e.Cache.Get(host); ok {
			// Cache hit
			ce := v.(cacheEntry)
			return ce.Action, ce.Arg, true, ipAddr, err
		}
		for _, entry := range e.Entries {
			if entry.MatchDomain(host) || (ipAddr != nil && entry.MatchIP(ipAddr.IP, e.GeoIPReader)) {
				e.Cache.Add(host, cacheEntry{entry.Action, entry.ActionArg})
				return entry.Action, entry.ActionArg, true, ipAddr, err
			}
		}
		e.Cache.Add(host, cacheEntry{e.DefaultAction, ""})
		return e.DefaultAction, "", true, ipAddr, err
	} else {
		// IP
		if v, ok := e.Cache.Get(ip.String()); ok {
			// Cache hit
			ce := v.(cacheEntry)
			return ce.Action, ce.Arg, false, &net.IPAddr{
				IP:   ip,
				Zone: zone,
			}, nil
		}
		for _, entry := range e.Entries {
			if entry.MatchIP(ip, e.GeoIPReader) {
				e.Cache.Add(ip.String(), cacheEntry{entry.Action, entry.ActionArg})
				return entry.Action, entry.ActionArg, false, &net.IPAddr{
					IP:   ip,
					Zone: zone,
				}, nil
			}
		}
		e.Cache.Add(ip.String(), cacheEntry{e.DefaultAction, ""})
		return e.DefaultAction, "", false, &net.IPAddr{
			IP:   ip,
			Zone: zone,
		}, nil
	}
}
