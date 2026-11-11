"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const { configuredHotfixPreviewBranch, evaluate } = require("./branch_flow.cjs");

test("작업 브랜치의 허용 대상은 preview뿐입니다", () => {
  for (const head of ["feat/order", "fix/socket", "docs/release"]) {
    for (const base of ["preview", "rc", "main", "feat/other"]) {
      assert.equal(evaluate({ head, base, sameRepository: true }).allowed, base === "preview");
    }
  }
});

test("저장소 내부 채널 승격은 한 단계씩만 가능합니다", () => {
  const expected = { preview: "rc", rc: "main", main: null };
  for (const [head, target] of Object.entries(expected)) {
    for (const base of ["preview", "rc", "main", "feat/other"]) {
      assert.equal(evaluate({ head, base, sameRepository: true }).allowed, base === target);
    }
  }
});

test("fork의 채널 이름으로 승격 권한을 가장할 수 없습니다", () => {
  for (const head of ["preview", "rc", "main", "feat/order"]) {
    for (const base of ["preview", "rc", "main"]) {
      assert.equal(evaluate({ head, base, sameRepository: false }).allowed, base === "preview");
    }
  }
});

test("RC 정책이 지정한 같은 저장소의 핫픽스 Preview만 rc로 승격합니다", () => {
  const head = "release/v0.4.4-hotfix-preview";
  const policy = {
    schema_version: "spine-cli.release-policy/v1",
    next_version: "v0.4.4",
    hotfix_preview_branch: head,
  };
  const hotfixPreviewBranch = configuredHotfixPreviewBranch(policy);
  assert.equal(hotfixPreviewBranch, head);
  assert.equal(configuredHotfixPreviewBranch({ ...policy, next_version: "v0.4.5" }), undefined);
  assert.equal(configuredHotfixPreviewBranch({ ...policy, next_version: "v0.04.4" }), undefined);
  assert.equal(evaluate({ head, base: "rc", sameRepository: true, hotfixPreviewBranch }).allowed, true);
  assert.equal(evaluate({ head, base: "rc", sameRepository: false, hotfixPreviewBranch }).allowed, false);
  assert.equal(evaluate({ head, base: "main", sameRepository: true, hotfixPreviewBranch }).allowed, false);
  assert.equal(evaluate({ head, base: "rc", sameRepository: true }).allowed, false);
});

test("서로 다른 대상 브랜치의 필수 상태를 공유하지 않습니다", () => {
  const contexts = ["preview", "rc", "main"].map(base => evaluate({ head: "feat/order", base, sameRepository: true }).context);
  assert.equal(new Set(contexts).size, 3);
});

test("기본 브랜치가 main인 초기 전환에서만 작업 PR을 main에 허용합니다", () => {
  assert.equal(evaluate({head: "ci/release-channels", base: "main", sameRepository: true, bootstrap: true}).allowed, true);
  assert.equal(evaluate({head: "ci/release-channels", base: "main", sameRepository: true}).allowed, false);
  assert.equal(evaluate({head: "preview", base: "main", sameRepository: true, bootstrap: true}).allowed, false);
});
