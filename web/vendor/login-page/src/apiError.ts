/** 与 goboost-fd lib/api.ts ApiFailure 同形（status/code/message），供包内错误映射。 */
export class ApiFailure extends Error {
  status: number;
  code: string;
  /** 429 限流时后端给的可重试时刻（ISO 8601）；其余错误缺省。 */
  retryAt?: string;
  constructor(message: string, status: number, code: string, retryAt?: string) {
    super(message);
    this.name = "ApiFailure";
    this.status = status;
    this.code = code;
    this.retryAt = retryAt;
  }
}
