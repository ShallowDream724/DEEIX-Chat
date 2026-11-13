"use client";

import * as React from "react";
import { useOptionalAuthSession } from "@/shared/auth/auth-session-context";
import { readAccessToken, SESSION_CLEARED_EVENT, SESSION_SNAPSHOT_CHANGED_EVENT } from "@/shared/auth/session";
import { clearThumbnailResources, clearUnusedThumbnailScopes, thumbnailScopeForUser } from "@/entities/file/lib/thumbnail-resource";

/** Mounted once per authenticated workspace, outside virtualized attachment rows. */
export function FileResourceLifecycle() {
  const session = useOptionalAuthSession();
  const scope = session?.user && session.userStatus === "ready" ? thumbnailScopeForUser(session.user.id) : null;

  React.useEffect(() => {
    if (scope) clearUnusedThumbnailScopes(scope);
  }, [scope]);

  React.useEffect(() => {
    const clearIfSignedOut = () => { if (!readAccessToken()) clearThumbnailResources(); };
    window.addEventListener(SESSION_SNAPSHOT_CHANGED_EVENT, clearIfSignedOut);
    window.addEventListener(SESSION_CLEARED_EVENT, clearThumbnailResources);
    clearIfSignedOut();
    return () => {
      window.removeEventListener(SESSION_SNAPSHOT_CHANGED_EVENT, clearIfSignedOut);
      window.removeEventListener(SESSION_CLEARED_EVENT, clearThumbnailResources);
    };
  }, []);

  return null;
}
