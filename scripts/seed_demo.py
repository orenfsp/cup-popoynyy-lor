#!/usr/bin/env python3
"""Create a small, meaningful demo dataset in an empty Molva database."""

from __future__ import annotations

import http.cookiejar
import json
from pathlib import Path
import urllib.error
import urllib.request


PUBLIC_API = "http://localhost:8088/api/v1"
STAFF_API = "http://localhost:8089/api/v1"
PASSWORD = "Molva-local-2026!"
EXPERT_ID = "00000000-0000-4000-8000-000000000002"


class Client:
    def __init__(self, base: str):
        self.base = base
        jar = http.cookiejar.CookieJar()
        self.http = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(jar)
        )

    def request(self, path: str, payload=None, expected: int = 200):
        data = None if payload is None else json.dumps(payload).encode("utf-8")
        request = urllib.request.Request(
            self.base + path,
            data=data,
            method="GET" if payload is None else "POST",
            headers={
                "Content-Type": "application/json",
                "X-Molva-Request": "1",
            },
        )
        try:
            response = self.http.open(request)
        except urllib.error.HTTPError as error:
            body = error.read().decode("utf-8")
            raise RuntimeError(f"{path}: HTTP {error.code}: {body}") from error
        body = response.read().decode("utf-8")
        if response.status != expected:
            raise RuntimeError(
                f"{path}: ожидался HTTP {expected}, получен {response.status}: {body}"
            )
        return json.loads(body) if body else None


def login(email: str) -> Client:
    client = Client(STAFF_API)
    client.request("/staff/login", {"email": email, "password": PASSWORD})
    return client


def create_appeal(client: Client, **values):
    response = client.request("/appeals", values, expected=201)
    return {
        "id": response["appeal"]["id"],
        "publicId": response["publicId"],
        "accessCode": response["accessCode"],
        "scenario": values["body"],
    }


def action(client: Client, appeal_id: str, name: str, **values):
    client.request(
        f"/staff/cases/{appeal_id}/actions",
        {"action": name, **values},
        expected=204,
    )


def message(client: Client, appeal_id: str, body: str):
    client.request(
        f"/staff/cases/{appeal_id}/messages", {"body": body}, expected=201
    )


def main():
    operator = login("operator@molva.local")
    expert = login("expert@molva.local")

    existing = operator.request("/staff/cases")["items"]
    if existing:
        raise SystemExit(
            "База уже содержит обращения. Для чистого наполнения выполните "
            "`docker compose down -v`, затем снова поднимите сервис."
        )

    public = Client(PUBLIC_API)
    common = {"email": ""}
    cases = []

    cyberbullying = create_appeal(
        public,
        applicantType="STUDENT",
        categoryCode="cyberbullying",
        body=(
            "Одноклассники создали чат, публикуют там мои фотографии без "
            "разрешения и пишут обидные комментарии. Я боюсь идти в школу."
        ),
        answers={
            "where_happens": "В интернете",
            "how_long": "Несколько недель",
            "asked_help": "Нет",
        },
        **common,
    )
    cases.append(cyberbullying)
    action(operator, cyberbullying["id"], "priority", value="URGENT")
    action(operator, cyberbullying["id"], "assign", expertId=EXPERT_ID)
    message(
        expert,
        cyberbullying["id"],
        "Спасибо, что рассказал об этом. Давай сначала разберём, где "
        "опубликованы материалы и кто может помочь сохранить доказательства.",
    )
    action(expert, cyberbullying["id"], "status", value="IN_PROGRESS")

    teacher_conflict = create_appeal(
        public,
        applicantType="PARENT",
        categoryCode="teacher_conflict",
        body=(
            "Учитель регулярно повышает голос на сына перед классом и "
            "обсуждает его оценки с другими родителями. Хотим решить ситуацию спокойно."
        ),
        answers={
            "where_happens": "В школе",
            "how_long": "Несколько недель",
            "asked_help": "Да",
        },
        email="parent.demo@example.test",
    )
    cases.append(teacher_conflict)
    message(
        operator,
        teacher_conflict["id"],
        "Уточните, пожалуйста, обращались ли вы к классному руководителю "
        "или администрации школы и какой ответ получили?",
    )

    danger = create_appeal(
        public,
        applicantType="TEACHER",
        categoryCode="threats",
        body=(
            "Ученик сказал после урока: «Меня бьют дома». Он просит никому "
            "не говорить и боится возвращаться домой сегодня вечером."
        ),
        answers={
            "where_happens": "Дома",
            "how_long": "Давно",
            "asked_help": "Нет",
        },
        **common,
    )
    cases.append(danger)

    legal = create_appeal(
        public,
        applicantType="PARENT",
        categoryCode="legal",
        body=(
            "Нужна консультация: школа разместила фотографию ребёнка на сайте "
            "без нашего согласия. Хотим понять, как правильно попросить удалить её."
        ),
        answers={
            "where_happens": "В интернете",
            "how_long": "Несколько дней",
            "asked_help": "Да",
        },
        **common,
    )
    cases.append(legal)
    action(operator, legal["id"], "assign", expertId=EXPERT_ID)
    message(
        expert,
        legal["id"],
        "Уточните, пожалуйста, направляли ли вы школе письменный запрос и "
        "сохранился ли скриншот страницы с фотографией?",
    )
    action(expert, legal["id"], "status", value="WAITING_FOR_APPLICANT")

    output = Path(__file__).resolve().parent.parent / "demo-access-codes.json"
    output.write_text(
        json.dumps({"appeals": cases}, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    print(f"Создано осмысленных обращений: {len(cases)}")
    print(f"Коды заявителей: {output}")


if __name__ == "__main__":
    main()
