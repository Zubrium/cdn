import { describe, expect, it } from "vitest";
import { ApiError } from "../../../../../frontend/admin/src/api";
import { errorMessage } from "../../../../../frontend/admin/src/lib/errors";

describe("errorMessage", () => {
  it("shows server-provided messages when they are meaningful", () => {
    expect(errorMessage(new ApiError(400, "invalid_request", "Поле key обязательно"))).toBe("Поле key обязательно");
  });

  it("localizes bare API error codes instead of showing raw identifiers", () => {
    expect(errorMessage(new ApiError(400, "invalid_request", "invalid_request"))).toBe(
      "Некорректный запрос. Проверьте введённые данные.",
    );
    expect(errorMessage(new ApiError(503, "unavailable", "unavailable"))).toBe(
      "Сервис временно недоступен. Повторите попытку позже.",
    );
  });

  it("keeps a generic fallback for unknown codes and non-API errors", () => {
    expect(errorMessage(new ApiError(418, "teapot", "teapot"))).toBe("Запрос завершился с ошибкой. Повторите попытку.");
    expect(errorMessage(new Error("boom"))).toBe("boom");
  });
});
