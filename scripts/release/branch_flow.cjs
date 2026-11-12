"use strict";

// fork의 main 등은 작업 브랜치로 취급하고, 저장소 내부 채널 이름만 예약합니다.
function configuredHotfixPreviewBranch(policy) {
  if (policy?.schema_version !== "spine-cli.release-policy/v1" ||
      typeof policy.next_version !== "string" ||
      !/^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(policy.next_version)) return undefined;
  const branch = `release/${policy.next_version}-hotfix-preview`;
  return policy.hotfix_preview_branch === branch ? branch : undefined;
}

function evaluate({ head, base, sameRepository, bootstrap = false, hotfixPreviewBranch }) {
  let allowed = false;
  if (bootstrap && base === "main" && !["preview", "rc", "main"].includes(head)) allowed = true;
  else if (sameRepository && head === "preview") allowed = base === "rc";
  else if (sameRepository && head === "rc") allowed = base === "main";
  else if (sameRepository && hotfixPreviewBranch && head === hotfixPreviewBranch) allowed = base === "rc";
  else if (!(sameRepository && head === "main")) allowed = base === "preview";
  return {
    allowed,
    context: `Branch Flow / ${base}`,
    description: allowed
      ? `${head} → ${base} 허용`
      : "작업 → preview → rc → main 방향만 허용합니다",
  };
}

module.exports = { configuredHotfixPreviewBranch, evaluate };

if (require.main === module) {
  if (process.env.GITHUB_EVENT_NAME === "pull_request") {
    const event = JSON.parse(require("node:fs").readFileSync(process.env.GITHUB_EVENT_PATH, "utf8"));
    const pr = event.pull_request;
    const sameRepository = pr.head.repo?.full_name === pr.base.repo.full_name;
    const hotfixPreviewBranch = pr.base.ref === "rc" && sameRepository
      ? configuredHotfixPreviewBranch(JSON.parse(require("node:fs").readFileSync(".github/release-policy.json", "utf8")))
      : undefined;
    const result = evaluate({
      head: pr.head.ref,
      base: pr.base.ref,
      sameRepository,
      hotfixPreviewBranch,
      bootstrap: event.repository.default_branch === "main",
    });
    console.log(result.description);
    if (!result.allowed) process.exitCode = 1;
  } else {
    console.log("PR 외 CI 이벤트의 검증을 진행합니다");
  }
}
