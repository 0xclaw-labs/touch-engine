import { describe, expect, it } from "vitest";
import { codeIssueNotice, credentialIssueNotice } from "../src/lib/customer-journey";

describe("participation code notice", () => {
  it("returns null only when a safe payload exists", () => {
    expect(codeIssueNotice("https://touch.example.com/p/ptx_abc")).toBeNull();
    expect(codeIssueNotice("")).toContain("签发不出来");
    expect(codeIssueNotice("javascript:alert(1)")).toContain("签发不出来");
    expect(codeIssueNotice("https://touch.example.com/p/ptx_abc?x=1")).toContain("签发不出来");
  });

  it("accepts a staff-verify token as text, not a dead link", () => {
    expect(credentialIssueNotice("ptx_abc.def")).toBeNull();
    expect(credentialIssueNotice("")).toContain("签发不出来");
    expect(credentialIssueNotice("https://example.com/p/ptx_abc")).toContain("签发不出来");
  });
});
