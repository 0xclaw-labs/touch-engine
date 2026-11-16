export const TOUCH_APP_HOST = "";

// This module is also used by Edge middleware; keep it free of Node transports.
export function readSsoAppHost(env: Record<string, string | undefined> = process.env): string {
  const host = ((env.SSO_APP_HOST ?? "").trim() || TOUCH_APP_HOST).toLowerCase();
  return /^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(host) ? host : "";
}

export function hostAllowed(hostHeader: string | null, forwardedHost: string | null, appHost: string): boolean {
  if (!appHost) return false;
  const hosts = [hostHeader, forwardedHost].filter((h): h is string => !!h);
  if (hosts.length === 0) return false;
  return hosts.every((h) => {
    const bare = h.split(":")[0].toLowerCase();
    return bare === appHost || bare === `www.${appHost}`;
  });
}
