// Package visitor owns isolated public visitor runtimes and their evidence.
package visitor

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/sim"
)

type Config struct {
	Mode, Origin, AssetsDir, EvidenceDir string
	TrustedProxies                       []string
	Database                             db.Config
	WarehouseChargeTarget                int
	StartInWarehouseV2                   bool
}

func ConfigFromEnv() (Config, error) {
	database, err := db.ConfigFromEnv()
	if err != nil {
		return Config{}, err
	}
	c := Config{Mode: os.Getenv("FLEETOPS_MODE"), Origin: os.Getenv("FLEETOPS_PUBLIC_ORIGIN"),
		AssetsDir: os.Getenv("FLEETOPS_ASSETS_DIR"), EvidenceDir: os.Getenv("FLEETOPS_EVIDENCE_DIR"), Database: database}
	c.StartInWarehouseV2 = os.Getenv("FLEETOPS_START_WAREHOUSE_V2") == "1" ||
		c.Mode == "local" && os.Getenv("FLEETOPS_LOCAL_START_WAREHOUSE_V2") == "1"
	if value := os.Getenv("FLEETOPS_TRUSTED_PROXIES"); value != "" {
		c.TrustedProxies = strings.Split(value, ",")
	}
	if value, present := os.LookupEnv("FLEETOPS_WAREHOUSE_CHARGE_TARGET"); present {
		target, err := strconv.Atoi(value)
		if err != nil || target == 0 {
			return Config{}, errors.New("FLEETOPS_WAREHOUSE_CHARGE_TARGET must be a nonzero integer percentage")
		}
		c.WarehouseChargeTarget = target
	}
	return c, c.validate()
}

func (c Config) validate() error {
	invalid := errors.New("invalid visitor configuration: explicit mode, origin, assets and private evidence directories required")
	u, err := url.Parse(c.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || c.Origin != u.Scheme+"://"+u.Host || strings.ContainsAny(u.Host, " ,\\\t\r\n") {
		return invalid
	}
	if c.Mode != "local" && c.Mode != "production" {
		return invalid
	}
	if c.Mode == "production" && (u.Scheme != "https" || strings.Contains(strings.ToLower(u.Hostname()), "placeholder") || strings.HasSuffix(u.Hostname(), ".invalid")) {
		return invalid
	}
	if c.Mode == "local" && (u.Scheme != "http" || !net.ParseIP(u.Hostname()).IsLoopback() || len(c.TrustedProxies) != 0) {
		return invalid
	}
	if c.AssetsDir == "" || c.EvidenceDir == "" {
		return invalid
	}
	assets, err := filepath.Abs(c.AssetsDir)
	if err != nil {
		return invalid
	}
	evidence, err := filepath.Abs(c.EvidenceDir)
	if err != nil {
		return invalid
	}
	if relative, err := filepath.Rel(assets, evidence); err != nil || relative == "." || !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && relative != ".." {
		return invalid
	}
	if c.WarehouseChargeTarget != 0 && c.WarehouseChargeTarget != sim.WarehouseV2Config().ChargeRelease {
		return errors.New("FLEETOPS_WAREHOUSE_CHARGE_TARGET: this visitor release supports only the default 85%")
	}
	for _, cidr := range c.TrustedProxies {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return invalid
		}
		ones, _ := network.Mask.Size()
		if ones == 0 {
			return invalid
		}
	}
	return nil
}
