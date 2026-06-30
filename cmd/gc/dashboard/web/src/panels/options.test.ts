import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "../api";
import { syncCityScopeFromLocation } from "../state";
import { getOptions, invalidateOptions } from "./options";

describe("options cache", () => {
  beforeEach(() => {
    window.history.pushState({}, "", "/dashboard?city=mc-city");
    syncCityScopeFromLocation();
    invalidateOptions();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    invalidateOptions();
    window.history.pushState({}, "", "/dashboard");
    syncCityScopeFromLocation();
  });

  it("uses visible configured agents as compose recipients", async () => {
    const getSpy = vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/config") {
        return {
          data: {
            agents: [
              { name: "director", suspended: false },
              { name: "mayor", display_name: "The Mayor" },
              { name: "janitor", suspended: false, visibility: "background" },
            ],
          },
          error: undefined,
          request: undefined,
          response: undefined,
        } as never;
      }
      if (path === "/v0/city/{cityName}/rigs") {
        return { data: { items: [] }, error: undefined, request: undefined, response: undefined } as never;
      }
      if (path === "/v0/city/{cityName}/beads") {
        return { data: { items: [] }, error: undefined, request: undefined, response: undefined } as never;
      }
      if (path === "/v0/city/{cityName}/mail") {
        return { data: { items: [] }, error: undefined, request: undefined, response: undefined } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    const options = await getOptions(true);

    expect(options.agents).toEqual(["director"]);
    expect(options.sessions).toEqual([
      { id: "director", label: "director", recipient: "director" },
    ]);
    expect(getSpy).not.toHaveBeenCalledWith(
      "/v0/city/{cityName}/sessions",
      expect.anything(),
    );
  });
});
