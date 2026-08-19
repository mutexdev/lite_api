package scripting

// The sandbox node:dns shim.
//
// Split out of scripting.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

func newScriptDNSObject(runtime *goja.Runtime) goja.Value {
	_ = runtime.Set("__liteApiDNSLookup", scriptDNSLookup)
	_ = runtime.Set("__liteApiDNSResolve", scriptDNSResolve)
	_ = runtime.Set("__liteApiDNSReverse", scriptDNSReverse)
	_ = runtime.Set("__liteApiDNSLookupService", scriptDNSLookupService)
	script := `(function () {
  const lookupBridge = globalThis.__liteApiDNSLookup;
  const resolveBridge = globalThis.__liteApiDNSResolve;
  const reverseBridge = globalThis.__liteApiDNSReverse;
  const lookupServiceBridge = globalThis.__liteApiDNSLookupService;
  let defaultResultOrder = "verbatim";
  let servers = [];

  function callbackRequired(callback) {
    if (typeof callback !== "function") {
      throw new TypeError("Callback must be a function");
    }
  }

  function normalizeLookupOptions(options) {
    if (typeof options === "number") return { family: options, all: false };
    if (!options || typeof options !== "object") return { family: 0, all: false };
    const family = Number(options.family || 0);
    return { family: family === 4 || family === 6 ? family : 0, all: !!options.all };
  }

  function callNode(callback, producer, spread) {
    callbackRequired(callback);
    try {
      const value = producer();
      if (spread && Array.isArray(value)) callback(null, ...value);
      else callback(null, value);
    } catch (err) {
      callback(err);
    }
  }

  function asPromise(producer) {
    try {
      return Promise.resolve(producer());
    } catch (err) {
      return Promise.reject(err);
    }
  }

  function lookupResult(hostname, options) {
    const normalized = normalizeLookupOptions(options);
    const result = lookupBridge(String(hostname || ""), normalized.family);
    if (normalized.all) return result.addresses || [];
    return { address: result.address, family: result.family };
  }

  function lookup(hostname, options, callback) {
    if (typeof options === "function") {
      callback = options;
      options = undefined;
    }
    callNode(callback, function () {
      const result = lookupResult(hostname, options);
      return Array.isArray(result) ? [result] : [result.address, result.family];
    }, true);
  }

  function resolveResult(hostname, rrtype) {
    return resolveBridge(String(hostname || ""), String(rrtype || "A").toUpperCase());
  }

  function resolve(hostname, rrtype, callback) {
    if (typeof rrtype === "function") {
      callback = rrtype;
      rrtype = "A";
    }
    callNode(callback, function () { return resolveResult(hostname, rrtype || "A"); });
  }

  function resolveTyped(rrtype) {
    return function (hostname, options, callback) {
      if (typeof options === "function") {
        callback = options;
      }
      callNode(callback, function () { return resolveResult(hostname, rrtype); });
    };
  }

  const resolve4 = resolveTyped("A");
  const resolve6 = resolveTyped("AAAA");
  const resolveCname = resolveTyped("CNAME");
  const resolveTxt = resolveTyped("TXT");
  const resolveMx = resolveTyped("MX");
  const resolveNs = resolveTyped("NS");
  const resolveSrv = resolveTyped("SRV");
  const resolvePtr = resolveTyped("PTR");
  const resolveAny = resolveTyped("ANY");

  function reverse(ip, callback) {
    callNode(callback, function () { return reverseBridge(String(ip || "")); });
  }

  function lookupService(address, port, callback) {
    callNode(callback, function () {
      const result = lookupServiceBridge(String(address || ""), Number(port || 0));
      return [result.hostname, result.service];
    }, true);
  }

  function getServers() {
    return servers.slice();
  }

  function setServers(nextServers) {
    if (!Array.isArray(nextServers)) {
      throw new TypeError("servers must be an array");
    }
    servers = nextServers.map(String);
  }

  function getDefaultResultOrder() {
    return defaultResultOrder;
  }

  function setDefaultResultOrder(order) {
    const value = String(order || "");
    if (value !== "ipv4first" && value !== "verbatim") {
      throw new TypeError("dns result order must be 'ipv4first' or 'verbatim'");
    }
    defaultResultOrder = value;
  }

  const promises = {
    lookup: function (hostname, options) {
      return asPromise(function () { return lookupResult(hostname, options); });
    },
    resolve: function (hostname, rrtype) {
      return asPromise(function () { return resolveResult(hostname, rrtype || "A"); });
    },
    resolve4: function (hostname, options) {
      return asPromise(function () { return resolveResult(hostname, "A"); });
    },
    resolve6: function (hostname, options) {
      return asPromise(function () { return resolveResult(hostname, "AAAA"); });
    },
    resolveCname: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "CNAME"); });
    },
    resolveTxt: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "TXT"); });
    },
    resolveMx: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "MX"); });
    },
    resolveNs: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "NS"); });
    },
    resolveSrv: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "SRV"); });
    },
    resolvePtr: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "PTR"); });
    },
    resolveAny: function (hostname) {
      return asPromise(function () { return resolveResult(hostname, "ANY"); });
    },
    reverse: function (ip) {
      return asPromise(function () { return reverseBridge(String(ip || "")); });
    },
    lookupService: function (address, port) {
      return asPromise(function () { return lookupServiceBridge(String(address || ""), Number(port || 0)); });
    },
    getServers,
    setServers
  };

  function Resolver() {}
  Resolver.prototype.lookup = lookup;
  Resolver.prototype.resolve = resolve;
  Resolver.prototype.resolve4 = resolve4;
  Resolver.prototype.resolve6 = resolve6;
  Resolver.prototype.resolveCname = resolveCname;
  Resolver.prototype.resolveTxt = resolveTxt;
  Resolver.prototype.resolveMx = resolveMx;
  Resolver.prototype.resolveNs = resolveNs;
  Resolver.prototype.resolveSrv = resolveSrv;
  Resolver.prototype.resolvePtr = resolvePtr;
  Resolver.prototype.resolveAny = resolveAny;
  Resolver.prototype.reverse = reverse;
  Resolver.prototype.lookupService = lookupService;
  Resolver.prototype.getServers = getServers;
  Resolver.prototype.setServers = setServers;

  return {
    ADDRCONFIG: 32,
    V4MAPPED: 8,
    NODATA: "ENODATA",
    FORMERR: "EFORMERR",
    SERVFAIL: "ESERVFAIL",
    NOTFOUND: "ENOTFOUND",
    NOTIMP: "ENOTIMP",
    REFUSED: "EREFUSED",
    BADQUERY: "EBADQUERY",
    BADNAME: "EBADNAME",
    BADFAMILY: "EBADFAMILY",
    BADRESP: "EBADRESP",
    CONNREFUSED: "ECONNREFUSED",
    TIMEOUT: "ETIMEOUT",
    EOF: "EOF",
    FILE: "EFILE",
    NOMEM: "ENOMEM",
    DESTRUCTION: "EDESTRUCTION",
    BADSTR: "EBADSTR",
    BADFLAGS: "EBADFLAGS",
    NONAME: "ENONAME",
    BADHINTS: "EBADHINTS",
    NOTINITIALIZED: "ENOTINITIALIZED",
    LOADIPHLPAPI: "ELOADIPHLPAPI",
    ADDRGETNETWORKPARAMS: "EADDRGETNETWORKPARAMS",
    CANCELLED: "ECANCELLED",
    lookup,
    lookupService,
    resolve,
    resolve4,
    resolve6,
    resolveCname,
    resolveTxt,
    resolveMx,
    resolveNs,
    resolveSrv,
    resolvePtr,
    resolveAny,
    reverse,
    getServers,
    setServers,
    getDefaultResultOrder,
    setDefaultResultOrder,
    Resolver,
    promises
  };
})()`
	value, err := runtime.RunProgram(scriptDNSShim.compiled(script))
	if err != nil {
		panic(runtime.NewGoError(err))
	}
	_ = runtime.Set("__liteApiDNSLookup", goja.Undefined())
	_ = runtime.Set("__liteApiDNSResolve", goja.Undefined())
	_ = runtime.Set("__liteApiDNSReverse", goja.Undefined())
	_ = runtime.Set("__liteApiDNSLookupService", goja.Undefined())
	return value
}

func scriptDNSLookup(hostname string, family int) (map[string]interface{}, error) {
	records, err := scriptDNSLookupRecords(hostname, family)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("getaddrinfo ENOTFOUND %s", strings.TrimSpace(hostname))
	}
	first := records[0]
	return map[string]interface{}{
		"address":   first["address"],
		"family":    first["family"],
		"addresses": records,
	}, nil
}

func scriptDNSLookupRecords(hostname string, family int) ([]map[string]interface{}, error) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return nil, errors.New("hostname is required")
	}
	if family != 0 && family != 4 && family != 6 {
		return nil, fmt.Errorf("invalid DNS address family %d", family)
	}
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return nil, err
	}
	records := make([]map[string]interface{}, 0, len(ips))
	seen := map[string]bool{}
	for _, ip := range ips {
		recordFamily := 6
		address := ip.String()
		if ipv4 := ip.To4(); ipv4 != nil {
			recordFamily = 4
			address = ipv4.String()
		}
		if family != 0 && recordFamily != family {
			continue
		}
		if seen[address] {
			continue
		}
		seen[address] = true
		records = append(records, map[string]interface{}{
			"address": address,
			"family":  recordFamily,
		})
	}
	return records, nil
}

func scriptDNSResolve(hostname, rrtype string) (interface{}, error) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return nil, errors.New("hostname is required")
	}
	switch strings.ToUpper(strings.TrimSpace(rrtype)) {
	case "", "A":
		return scriptDNSResolveAddresses(hostname, 4)
	case "AAAA":
		return scriptDNSResolveAddresses(hostname, 6)
	case "CNAME":
		cname, err := net.LookupCNAME(hostname)
		if err != nil {
			return nil, err
		}
		return []string{strings.TrimSuffix(cname, ".")}, nil
	case "TXT":
		records, err := net.LookupTXT(hostname)
		if err != nil {
			return nil, err
		}
		out := make([][]string, 0, len(records))
		for _, record := range records {
			out = append(out, []string{record})
		}
		return out, nil
	case "MX":
		records, err := net.LookupMX(hostname)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]interface{}, 0, len(records))
		for _, record := range records {
			out = append(out, map[string]interface{}{
				"exchange": strings.TrimSuffix(record.Host, "."),
				"priority": record.Pref,
			})
		}
		return out, nil
	case "NS":
		records, err := net.LookupNS(hostname)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(records))
		for _, record := range records {
			out = append(out, strings.TrimSuffix(record.Host, "."))
		}
		return out, nil
	case "SRV":
		service, proto, name := scriptDNSSRVParts(hostname)
		_, records, err := net.LookupSRV(service, proto, name)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]interface{}, 0, len(records))
		for _, record := range records {
			out = append(out, map[string]interface{}{
				"name":     strings.TrimSuffix(record.Target, "."),
				"port":     record.Port,
				"priority": record.Priority,
				"weight":   record.Weight,
			})
		}
		return out, nil
	case "PTR":
		return scriptDNSReverse(hostname)
	case "ANY":
		addresses, err := scriptDNSResolveAddresses(hostname, 0)
		if err != nil {
			return nil, err
		}
		return addresses, nil
	default:
		return nil, fmt.Errorf("unsupported DNS record type %q", rrtype)
	}
}

func scriptDNSResolveAddresses(hostname string, family int) ([]string, error) {
	records, err := scriptDNSLookupRecords(hostname, family)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, fmt.Sprint(record["address"]))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("queryA ENODATA %s", hostname)
	}
	return out, nil
}

func scriptDNSReverse(ip string) ([]string, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return nil, errors.New("IP address is required")
	}
	records, err := net.LookupAddr(ip)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, strings.TrimSuffix(record, "."))
	}
	return out, nil
}

func scriptDNSLookupService(address string, port int) (map[string]interface{}, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, errors.New("IP address is required")
	}
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d", port)
	}
	hostnames, err := net.LookupAddr(address)
	if err != nil {
		return nil, err
	}
	hostname := address
	if len(hostnames) > 0 {
		hostname = strings.TrimSuffix(hostnames[0], ".")
	}
	return map[string]interface{}{
		"hostname": hostname,
		"service":  strconv.Itoa(port),
	}, nil
}

func scriptDNSSRVParts(hostname string) (string, string, string) {
	trimmed := strings.Trim(hostname, ".")
	parts := strings.Split(trimmed, ".")
	if len(parts) >= 3 && strings.HasPrefix(parts[0], "_") && strings.HasPrefix(parts[1], "_") {
		return strings.TrimPrefix(parts[0], "_"), strings.TrimPrefix(parts[1], "_"), strings.Join(parts[2:], ".")
	}
	return "", "", hostname
}
