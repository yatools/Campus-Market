from __future__ import annotations

import logging
from typing import Any

from fastapi import FastAPI, HTTPException, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse


class APIError(HTTPException):
    def __init__(self, status_code: int, code: str, message: str, field_errors: dict[str, str] | None = None):
        super().__init__(status_code=status_code, detail=message)
        self.code = code
        self.message = message
        self.field_errors = field_errors or {}


def error_payload(request: Request, code: str, message: str, field_errors: Any = None) -> dict[str, Any]:
    return {
        "code": code,
        "message": message,
        "field_errors": field_errors or {},
        "request_id": getattr(request.state, "request_id", ""),
    }


def install_error_handlers(app: FastAPI) -> None:
    @app.exception_handler(APIError)
    async def api_error_handler(request: Request, exc: APIError) -> JSONResponse:
        return JSONResponse(
            status_code=exc.status_code,
            content=error_payload(request, exc.code, exc.message, exc.field_errors),
        )

    @app.exception_handler(RequestValidationError)
    async def validation_error_handler(request: Request, exc: RequestValidationError) -> JSONResponse:
        fields: dict[str, str] = {}
        for item in exc.errors():
            key = ".".join(str(x) for x in item["loc"] if x not in ("body", "query", "path"))
            fields[key or "request"] = item["msg"]
        return JSONResponse(
            status_code=422,
            content=error_payload(request, "VALIDATION_ERROR", "提交内容不符合要求", fields),
        )

    @app.exception_handler(HTTPException)
    async def http_error_handler(request: Request, exc: HTTPException) -> JSONResponse:
        return JSONResponse(
            status_code=exc.status_code,
            content=error_payload(request, "HTTP_ERROR", str(exc.detail)),
        )

    @app.exception_handler(Exception)
    async def unexpected_error_handler(request: Request, exc: Exception) -> JSONResponse:
        logging.getLogger("wutong").exception(
            "unhandled_error path=%s request_id=%s",
            request.url.path,
            getattr(request.state, "request_id", ""),
            exc_info=(type(exc), exc, exc.__traceback__),
        )
        return JSONResponse(
            status_code=500,
            content=error_payload(request, "INTERNAL_ERROR", "服务器暂时无法处理该请求"),
        )
