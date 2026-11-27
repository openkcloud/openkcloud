// 포털 주소 밑(/auth/realms/…, 테스트배드)으로 열린 로그인 화면이면 주소창을 /auth/login/ 로 보이게 한다.
// 요청·폼 제출은 그대로 Keycloak 으로 간다(보이는 주소만 바꿈). 새로고침하면 포털 /auth/login 이 로그인을 처음부터 다시 시작한다.
// 개발(Keycloak 이 별도 주소 /realms/…)에서는 바꾸지 않는다 — 다른 주소의 경로를 보여 주게 되므로.
// 문서를 다 읽은 뒤 바꾼다 — 비밀번호 제출(POST) 결과 화면은 읽는 도중에 바꾸면 반영되지 않는다.
(function () {
  function show() {
    if (location.pathname.indexOf("/realms/") > 0 && window.history && history.replaceState) {
      history.replaceState(history.state, "", "/auth/login/");
    }
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", show);
  else show();
})();
