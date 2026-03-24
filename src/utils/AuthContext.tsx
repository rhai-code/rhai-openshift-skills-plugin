import * as React from 'react';
import { getAuthInfo, AuthInfo } from './api';

interface AuthState {
  username: string;
  isAdmin: boolean;
  loading: boolean;
}

// Module-level cache so all pages share the same auth state
let cachedAuth: AuthInfo | null = null;
let authPromise: Promise<AuthInfo> | null = null;

function fetchAuth(): Promise<AuthInfo> {
  if (!authPromise) {
    authPromise = getAuthInfo()
      .then((info) => {
        cachedAuth = info;
        return info;
      })
      .catch(() => {
        const fallback: AuthInfo = { username: '', groups: [], is_admin: false };
        cachedAuth = fallback;
        return fallback;
      });
  }
  return authPromise;
}

export function useAuth(): AuthState {
  const [auth, setAuth] = React.useState<AuthState>(() => {
    if (cachedAuth) {
      return { username: cachedAuth.username, isAdmin: cachedAuth.is_admin, loading: false };
    }
    return { username: '', isAdmin: false, loading: true };
  });

  React.useEffect(() => {
    if (cachedAuth) {
      setAuth({ username: cachedAuth.username, isAdmin: cachedAuth.is_admin, loading: false });
      return;
    }
    fetchAuth().then((info) => {
      setAuth({ username: info.username, isAdmin: info.is_admin, loading: false });
    });
  }, []);

  return auth;
}
