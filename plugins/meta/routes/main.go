package main

import (
	"encoding/json"
	"fmt"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/netlinksafe"
	"github.com/containernetworking/plugins/pkg/ns"
	bv "github.com/containernetworking/plugins/pkg/utils/buildversion"
	"github.com/vishvananda/netlink"
)

type RoutesNetConf struct {
	types.NetConf

	ReplaceRoutes []ReplaceRoute `json:"replaceroutes"`
}

type ReplaceRoute struct {
	OldRoute  *types.Route   `json:"oldroute"`
	NewRoutes []*types.Route `json:"newroutes,omitempty"`
}

func parseConf(data []byte) (*RoutesNetConf, error) {
	conf := RoutesNetConf{}
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}

	return &conf, nil
}

func getRoute(netns ns.NetNS, r *types.Route) (netlink.Route, error) {
	var route netlink.Route

	err := netns.Do(func(_ ns.NetNS) error {
		family := netlink.FAMILY_V4
		isV4 := r.Dst.IP.To4() != nil
		if !isV4 {
			family = netlink.FAMILY_V6
		}

		filter := &netlink.Route{Dst: &r.Dst}
		if r.Dst.String() == "0.0.0.0/0" || r.Dst.String() == "::/0" {
			filter.Dst = nil
		}

		routes, err := netlinksafe.RouteListFiltered(family, filter, netlink.RT_FILTER_DST)
		if err != nil {
			return fmt.Errorf("failed to get default route in %q: %v", netns.Path(), err)
		}
		if len(routes) > 1 {
			return fmt.Errorf("%d routes found for %s", len(routes), r.Dst.String())
		} else if len(routes) == 0 {
			return fmt.Errorf("no route found for %s in %q", r.Dst.String(), netns.Path())
		}
		route = routes[0]

		return nil
	})
	return route, err
}

func processReplaceRoute(netns ns.NetNS, replaceRoute ReplaceRoute, result *current.Result) error {
	err := netns.Do(func(_ ns.NetNS) error {
		route, err := getRoute(netns, replaceRoute.OldRoute)
		if err != nil {
			return err
		}

		for _, r := range replaceRoute.NewRoutes {
			newRoute := &netlink.Route{
				LinkIndex: route.LinkIndex,
				Dst:       &r.Dst,
				Gw:        route.Gw,
			}
			err = netlink.RouteAdd(newRoute)
			if err != nil {
				return fmt.Errorf("failed to add route %s via %s: %v", newRoute.Dst.String(), newRoute.Gw.String(), err)
			}
			result.Routes = append(result.Routes, &types.Route{
				Dst: r.Dst,
				GW:  newRoute.Gw,
			})
		}

		err = netlink.RouteDel(&route)
		if err != nil {
			return fmt.Errorf("failed to delete route %s via %s: %v", route.Dst.String(), route.Gw.String(), err)
		}
		return nil
	})
	return err
}

func cmdAdd(args *skel.CmdArgs) error {
	result := &current.Result{}
	conf, err := parseConf(args.StdinData)
	if err != nil {
		return err
	}

	contNetns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer contNetns.Close()

	for _, replaceRoute := range conf.ReplaceRoutes {
		err := processReplaceRoute(contNetns, replaceRoute, result)
		if err != nil {
			return fmt.Errorf("failed to replace route %s: %v", replaceRoute.OldRoute.Dst.String(), err)
		}
	}

	return types.PrintResult(result, conf.CNIVersion)
}

func main() {
	skel.PluginMainFuncs(skel.CNIFuncs{
		Add: cmdAdd,
	}, version.VersionsStartingFrom("0.3.1"), bv.BuildString("routes"))
}
