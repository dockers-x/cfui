const DEFAULT_CFUI_CALLBACK_URL = "http://127.0.0.1:14333/oauth/callback";
const CALLBACK_PATH = "/oauth/callback";
const CALLBACK_URL_PARAM = "cfui_callback_url";
const STATE_PREFIX = "cfui1.";
const RELAY_VERSION = "state-v1";
const OAUTH_QUERY_PARAMS = ["code", "state", "error", "error_description", "error_uri"];

// Optional Worker variables:
// - CFUI_CALLBACK_URL: fallback target, for example https://cfui.example.internal/oauth/callback.
// - CFUI_ALLOWED_CALLBACK_ORIGINS: comma-separated origins allowed for cfui_callback_url.
//   Use "*" only when this Worker intentionally serves multiple cfui domains.
//   Without this allowlist, dynamic callbacks are limited to loopback, private/LAN,
//   .local, .internal, .lan, .home.arpa, and .test hosts.
//
// Cloudflare OAuth Client redirect URI:
// - Register only this Worker's public HTTPS callback, for example https://oauth.example.com/oauth/callback.
// - Do not append cfui_callback_url to the Cloudflare OAuth Client redirect URI.

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (request.method === "OPTIONS") {
      return noContent();
    }
    if (request.method !== "GET" && request.method !== "HEAD") {
      return text("Method not allowed", 405, {
        Allow: "GET, HEAD, OPTIONS",
      });
    }
    if (url.pathname === "/health") {
      return text(`ok ${RELAY_VERSION}`, 200, {
        "X-CFUI-OAuth-Relay": RELAY_VERSION,
      });
    }
    if (url.pathname === "/") {
      return text("cfui OAuth relay is running. Register /oauth/callback as the Cloudflare OAuth redirect URI.", 200);
    }
    if (url.pathname !== CALLBACK_PATH) {
      return text("Not found", 404);
    }

    const state = url.searchParams.get("state");
    const code = url.searchParams.get("code");
    const oauthError = url.searchParams.get("error");
    if (!state || (!code && !oauthError)) {
      return text("Missing OAuth code/error or state", 400);
    }

    let target;
    try {
      target = callbackURL(url, env);
    } catch (error) {
      return text(error.message, 500);
    }

    for (const name of OAUTH_QUERY_PARAMS) {
      const value = url.searchParams.get(name);
      if (value) {
        target.searchParams.set(name, value);
      }
    }

    return new Response(null, {
      status: 302,
      headers: {
        Location: target.toString(),
        "Cache-Control": "no-store",
        "Referrer-Policy": "no-referrer",
      },
    });
  },
};

function callbackURL(requestURL, env) {
  const stateCallback = callbackURLFromState(requestURL.searchParams.get("state"));
  const callbackParam = String(requestURL.searchParams.get(CALLBACK_URL_PARAM) || "").trim();
  const dynamic = stateCallback !== "" || callbackParam !== "";
  const raw = String(stateCallback || callbackParam || env.CFUI_CALLBACK_URL || DEFAULT_CFUI_CALLBACK_URL).trim();
  const target = new URL(raw);
  if (target.protocol !== "http:" && target.protocol !== "https:") {
    throw new Error("cfui callback URL must use http or https");
  }
  if (target.username || target.password) {
    throw new Error("cfui callback URL must not include credentials");
  }
  if (target.pathname !== CALLBACK_PATH) {
    throw new Error(`cfui callback URL path must be ${CALLBACK_PATH}`);
  }
  if (dynamic && !isParamCallbackAllowed(target, env)) {
    throw new Error("dynamic cfui callback URL is not allowed by this Worker");
  }
  target.search = "";
  target.hash = "";
  return target;
}

function callbackURLFromState(state) {
  const raw = String(state || "").trim();
  if (!raw.startsWith(STATE_PREFIX)) {
    return "";
  }
  try {
    const payload = JSON.parse(base64URLDecode(raw.slice(STATE_PREFIX.length)));
    return String(payload.u || "").trim();
  } catch {
    return "";
  }
}

function base64URLDecode(value) {
  const base64 = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = base64 + "=".repeat((4 - (base64.length % 4)) % 4);
  return atob(padded);
}

function isParamCallbackAllowed(target, env) {
  for (const origin of allowedCallbackOrigins(env)) {
    if (origin === "*" || origin === target.origin) {
      return true;
    }
  }
  return isLocalCallbackHost(target.hostname);
}

function allowedCallbackOrigins(env) {
  return String(env.CFUI_ALLOWED_CALLBACK_ORIGINS || "")
    .split(",")
    .map((value) => normalizeOrigin(value.trim()))
    .filter(Boolean);
}

function normalizeOrigin(value) {
  if (!value) {
    return "";
  }
  if (value === "*") {
    return "*";
  }
  try {
    const url = new URL(value);
    if (url.protocol !== "http:" && url.protocol !== "https:") {
      return "";
    }
    return url.origin;
  } catch {
    return "";
  }
}

function isLocalCallbackHost(hostname) {
  const host = hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (
    host === "localhost" ||
    host.endsWith(".localhost") ||
    host.endsWith(".local") ||
    host.endsWith(".internal") ||
    host.endsWith(".lan") ||
    host.endsWith(".home.arpa") ||
    host.endsWith(".test")
  ) {
    return true;
  }
  if (host.includes(":")) {
    return host === "::1" || host.startsWith("fc") || host.startsWith("fd") || host.startsWith("fe80:");
  }
  const parts = host.split(".");
  if (parts.length !== 4) {
    return false;
  }
  const octets = parts.map((part) => Number(part));
  if (octets.some((octet, index) => !Number.isInteger(octet) || String(octet) !== parts[index] || octet < 0 || octet > 255)) {
    return false;
  }
  const [a, b] = octets;
  return (
    a === 10 ||
    a === 127 ||
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168) ||
    (a === 169 && b === 254)
  );
}

function noContent(headers = {}) {
  return new Response(null, {
    status: 204,
    headers: {
      "Cache-Control": "no-store",
      ...headers,
    },
  });
}

function text(body, status = 200, headers = {}) {
  return new Response(body, {
    status,
    headers: {
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
      ...headers,
    },
  });
}
