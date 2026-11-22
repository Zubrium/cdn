import type { FullResult, Reporter, TestCase, TestResult } from "@playwright/test/reporter";

const SCENARIO_ID = /E2E-[A-Z0-9]+(?:-[A-Z0-9]+)*-\d{3}/;

class E2ELineReporter implements Reporter {
  private readonly results: string[] = [];
  private ended = false;

  onTestEnd(test: TestCase, result: TestResult): void {
    const match = SCENARIO_ID.exec(test.title);
    if (!match) {
      this.results.push(`FAIL ${test.title} (scenario ID missing)`);
      return;
    }
    const id = match[0];
    if (result.status === "passed") {
      this.results.push(`PASS ${id}`);
      return;
    }
    if (result.status === "skipped") {
      this.results.push(`FAIL ${id} (skipped)`);
      return;
    }
    const detail = result.error?.message?.split("\n")[0] ?? result.status;
    this.results.push(`FAIL ${id} (${detail})`);
  }

  onEnd(result: FullResult): void {
    for (const line of this.results) {
      process.stdout.write(`${line}\n`);
    }
    if (this.results.length === 0) {
      process.stderr.write("FAIL no E2E tests executed\n");
      process.exitCode = 1;
      this.ended = true;
      return;
    }
    if (this.results.some((line) => line.startsWith("FAIL ")) || result.status !== "passed") {
      process.exitCode = 1;
    }
  }

  printsToStdio(): boolean {
    return true;
  }
}

export default E2ELineReporter;
