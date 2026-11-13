"use client";

import * as React from "react";

import { getMe } from "@/shared/api/auth";
import type { UserDTO } from "@/shared/api/auth-types";
import { USER_PROFILE_UPDATED_EVENT } from "@/shared/auth/user-profile-events";

export type AuthSessionUserStatus = "loading" | "ready" | "failed";
type UserSnapshot = { accessToken: string; user: UserDTO | null; userStatus: AuthSessionUserStatus };

/** Loads the signed-in user for `accessToken` and follows profile updates dispatched elsewhere in the app. */
export function useAuthUserLoader(accessToken: string) {
  const [snapshot, setSnapshot] = React.useState<UserSnapshot>({ accessToken, user: null, userStatus: "loading" });
  const request = React.useRef({ accessToken, revision: 0 });
  request.current.accessToken = accessToken;

  const refreshUser = React.useCallback(async () => {
    if (request.current.accessToken !== accessToken) return null;
    const revision = ++request.current.revision;
    setSnapshot((current) => ({ accessToken, user: current.accessToken === accessToken ? current.user : null, userStatus: "loading" }));
    try {
      const nextUser = await getMe(accessToken);
      if (request.current.accessToken !== accessToken || request.current.revision !== revision) return null;
      setSnapshot({ accessToken, user: nextUser, userStatus: "ready" });
      return nextUser;
    } catch {
      if (request.current.accessToken !== accessToken || request.current.revision !== revision) return null;
      setSnapshot({ accessToken, user: null, userStatus: "failed" });
      return null;
    }
  }, [accessToken]);

  React.useEffect(() => {
    void refreshUser();
    return () => {
      request.current.revision++;
    };
  }, [refreshUser]);

  React.useEffect(() => {
    function handleProfileUpdated(event: CustomEvent<UserDTO>) {
      const nextUser = event.detail;
      if (!nextUser) {
        return;
      }

      setSnapshot((current) => current.accessToken === request.current.accessToken && current.user?.id === nextUser.id
        ? { accessToken: current.accessToken, user: nextUser, userStatus: "ready" }
        : current);
    }

    window.addEventListener(USER_PROFILE_UPDATED_EVENT, handleProfileUpdated);
    return () => {
      window.removeEventListener(USER_PROFILE_UPDATED_EVENT, handleProfileUpdated);
    };
  }, []);

  // An access-token change renders before its loading effect. Never expose the
  // preceding account's identity in that render or let its late refresh win.
  const current = snapshot.accessToken === accessToken ? snapshot : { user: null, userStatus: "loading" as const };
  return { user: current.user, userStatus: current.userStatus, refreshUser };
}
