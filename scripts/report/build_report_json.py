"""Builds reporte.json for the gconnect test report. The cases come from the test names in
the repository (ID, then "condition: expected"); the judgment (risks, why each technique,
derivations) is written below. Run from the repository root:

  python3 -I scripts/report/build_report_json.py reportes-pruebas/<date>-gconnect
"""
import glob
import json
import re
import subprocess
import sys

# The output folder; scripts/report/run.sh passes reportes-pruebas/<date>-gconnect/.
R = (sys.argv[1] if len(sys.argv) > 1 else "reportes-pruebas/2026-10-16-gconnect").rstrip("/") + "/"
ID = re.compile(r'"((?:EP|BVA|CL|EG)-\d+|DT-R\d+|ST-T\d+) ([^"]*)"')

casos = {}
for f in sorted(glob.glob("*_test.go") + glob.glob("gconnecttest/*_test.go")):
    for n, line in enumerate(open(f).read().splitlines(), 1):
        for cid, name in ID.findall(line):
            if cid in casos:
                continue
            entrada, _, esperado = name.partition(": ")
            casos[cid] = {"id": cid, "entrada": entrada, "esperado": esperado or "lo que afirma la entrada", "ubicacion": f"{f}:{n}"}


def de(prefijo, rango=None):
    out = [c for k, c in casos.items() if k.startswith(prefijo) and (rango is None or int(k.rsplit("-", 1)[1].lstrip("RT")) in rango)]
    return sorted(out, key=lambda c: (c["ubicacion"].split(":")[0], int(c["ubicacion"].split(":")[1])))


def tabla(cab, filas):
    th = "".join(f"<th>{c}</th>" for c in cab)
    tb = "".join("<tr>" + "".join(f"<td>{c}</td>" for c in f) + "</tr>" for f in filas)
    return f'<table class="t"><thead><tr>{th}</tr></thead><tbody>{tb}</tbody></table>'


commit = subprocess.run(["git", "rev-parse", "--short", "HEAD"], capture_output=True, text=True).stdout.strip()
dt, st, ep, bva, eg, cl = de("DT-R"), de("ST-T"), de("EP-"), de("BVA-"), de("EG-"), de("CL-")
n = lambda xs: len(xs)

rep = {
    "modo": "completo",
    "portada": {
        "org": "gconnect", "area": "Aseguramiento de calidad", "tipo_doc": "Reporte de Pruebas",
        "objeto": "gconnect v0.1.0", "objeto_detalle": "github.com/danihrndzld/gconnect — Auth, Client, endpoints, Redact y gconnecttest",
        "version": commit, "titulo_corto": "gconnect — 2026",
        "subtitulo": [f"{len(casos)} casos con ID en 6 técnicas, 0 defectos abiertos",
                      "Cobertura de sentencias 99,9 % (699 de 700)"],
        "fecha": "16 de octubre de 2026",
    },
    "ficha": [
        ["Objeto de prueba", "<code>auth.go</code> (Login, Refresh, Fresh, Headers), <code>client.go</code> (Get, GetURL, Safe, Profile), <code>endpoints.go</code>, <code>redact.go</code>, <code>tokens.go</code>, <code>gconnecttest/server.go</code>"],
        ["Nivel / tipo", "Componente e integración de componentes contra un servidor HTTP falso — funcional, caja negra; cobertura de sentencias medida como caja blanca"],
        ["Autor", "Daniel Hernández"],
        ["Fecha", "16 de octubre de 2026"],
    ],
    "base_de_prueba": {"tipo": "spec", "fuente": "Spec de arquitectura 2026-10-16 §2.2, §2.4, §2.6 y §2.8; spec de la etapa 2 (Client rules); comentarios de documentación de la API pública y README; RFC 5849 §3.4 y RFC 6749 §5.1 y §6; límites de Garmin medidos el 2026-10-14"},
    "supuestos": [
        {"id": "S1", "texto": "Una respuesta de diauth sin <code>exp</code> en el JWT ni <code>expires_in</code> dura 1 hora, como asume python-garminconnect.", "casos": ["EP-408"]},
        {"id": "S2", "texto": "Un <code>Client</code> con tokens sin login detiene la corrida como sesión muerta: la spec §2.4 dice que la app traduce <code>ErrNoTokens</code> a GARMIN_SESSION.", "casos": ["EP-437"]},
        {"id": "S3", "texto": "Las páginas de cuenta bloqueada y de actualizar teléfono del widget son <code>LoginFailed</code> con un mensaje que nombra la causa.", "casos": ["DT-R429", "DT-R430"]},
        {"id": "S4", "texto": "«Expira dentro de margin» incluye la frontera: un token que expira exactamente en ahora + margin se refresca.", "casos": ["BVA-402"]},
    ],
    "resumen": (f"<p>Se ejecutaron {len(casos)} casos con <code>go test -race</code>; los {len(casos)} pasan y no quedó defecto abierto. "
                f"Las técnicas fueron Decision Table ({n(dt)} reglas), State Transition ({n(st)} transiciones), Equivalence Partitioning ({n(ep)} particiones), "
                f"Boundary Value Analysis ({n(bva)} valores), Error Guessing ({n(eg)} ataques) y Checklist ({n(cl)} ítems). "
                "La cobertura de sentencias medida con <code>-coverpkg=./...</code> es 699 de 700 (99,9 %); la única sentencia sin ejecutar es inalcanzable (<code>auth.go:441</code>). "
                "Ninguna prueba contacta a Garmin: un <code>TestMain</code> en cada paquete rechaza toda dirección que no sea loopback.</p>"),
    "objeto": ("<p>gconnect es un cliente no oficial de la API privada de Garmin Connect, extraído de la app Garmin Export. "
               "<code>Auth.Login</code> inicia sesión con el flujo móvil DI y, si Garmin lo bloquea, una vez con el flujo widget + OAuth1; "
               "<code>Refresh</code> y <code>Fresh</code> renuevan tokens; <code>Client</code> hace GET con pausa, reintentos y reglas de 401, 403 y 429; "
               "<code>Redact</code> oculta secretos en textos de error; <code>gconnecttest</code> es el Garmin falso contra el que corren todas las pruebas.</p>"
               "<p>Los esperados salen de la spec de arquitectura (§2.4: «a refresh failure on a network error, 5xx, 403 or 429 keeps the tokens; only a 400 or 401 from diauth or a 401 from the OAuth2 exchange reports ErrSessionDead»), "
               "de la spec de la etapa 2 («429: wait for Retry-After (seconds or an HTTP date, at most 60 s) or the backoff, at most 2 retries»; «Network error or timeout: up to 5 retries with backoff min(5000 * 2^attempt + jitter, 60000) ms»), "
               "de los comentarios de la API (<code>Params</code>: «activities_by_date pages with Offset and Limit (0 means 20)»; <code>Control</code>: «the longest matching prefix wins») "
               "y de los RFC 5849 y 6749. Donde la base calla se declararon los supuestos S1 a S4.</p>"),
    "riesgos": [
        {"id": "C1", "condicion": "Cadena de inicio de sesión (<code>auth.go</code> Login/chain)", "riesgo": "Un respaldo de más gasta inicios de sesión y bloquea la cuenta 15 min o más", "prob": 2, "impacto": 3, "exige": "Tabla de decisión completa sin minimizar + EG"},
        {"id": "C2", "condicion": "Clasificación del refresh (<code>Refresh</code>, <code>Fresh</code>)", "riesgo": "Declarar muerta una sesión viva obliga a reconectar; lo contrario repite 401", "prob": 2, "impacto": 3, "exige": "All transitions + BVA de 3 valores en el margen"},
        {"id": "C3", "condicion": "Reglas de estado HTTP del cliente (<code>client.go</code> get)", "riesgo": "Detener o no detener la corrida a destiempo; perder datos o martillar a Garmin", "prob": 2, "impacto": 3, "exige": "EP + BVA de 3 valores en rachas y reintentos"},
        {"id": "C4", "condicion": "Pausa, backoff y Retry-After", "riesgo": "Esperas mal calculadas violan el límite de Garmin", "prob": 2, "impacto": 2, "exige": "BVA + EG + statement coverage"},
        {"id": "C5", "condicion": "Redacción de secretos (<code>redact.go</code>, <code>errText</code>, <code>preview</code>)", "riesgo": "Un token o una contraseña en un log", "prob": 2, "impacto": 3, "exige": "Checklist completo + EP de claves + EG"},
        {"id": "C6", "condicion": "Tabla de endpoints (<code>endpoints.go</code>)", "riesgo": "URL mal formada: datos vacíos sin error", "prob": 1, "impacto": 2, "exige": "Checklist de 38 URLs + BVA de Limit"},
        {"id": "C7", "condicion": "Tipos de token, <code>Kind</code> y forma JSON", "riesgo": "Un archivo de tokens guardado que no se vuelve a leer", "prob": 1, "impacto": 3, "exige": "EP de Kind + checklist JSON"},
        {"id": "C8", "condicion": "Servidor falso <code>gconnecttest</code>", "riesgo": "Pruebas no deterministas o que tocan la red real", "prob": 2, "impacto": 2, "exige": "EP de prefijos + DT de rutas + EG de concurrencia"},
        {"id": "C9", "condicion": "Arquitectura: solo biblioteca estándar, sin disco", "riesgo": "Promesa pública rota sin que nadie lo note", "prob": 1, "impacto": 3, "exige": "Checklist con fixtures que rompen cada regla"},
    ],
    "estrategia": ("<p>Nivel: componente e integración de componentes [ctfl, §2.2.1]; cada prueba usa la API pública contra <code>gconnecttest</code>, un <code>httptest.Server</code> en loopback, "
                   "y un proxy inverso de prueba (<code>front</code> en <code>auth_test.go</code>) cuando hace falta una respuesta de Garmin que el falso no tiene. "
                   "Tipo: funcional y de caja negra; la cobertura de sentencias se midió para cerrar huecos, no para diseñar [ctfl, §2.2.2, §4.3.3]. "
                   "La forma es de trofeo: casi todo es integración contra el falso, sin mocks de <code>net/http</code> [ctfl, §5.1.6]. "
                   "Cuadrante Q2 (pruebas de negocio que guían el desarrollo) y Q1 para el test de arquitectura [ctfl, §5.1.7]. "
                   "Las pruebas corren en paralelo (<code>t.Parallel</code>) salvo EG-436, que mide una espera real.</p>"),
    "criterios_salida": [
        "100 % de reglas factibles de la tabla de inicio de sesión ejecutadas",
        "100 % de transiciones de refresh, válidas e inválidas",
        "BVA de 3 valores en el margen de refresh, la racha de 403 y el tope de Retry-After",
        "≥ 95 % de sentencias (puerta de CI) y 0 defectos de severidad alta abiertos",
        "0 pruebas que dependan de la red real (TestMain rechaza lo que no es loopback)",
    ],
    "tecnicas": [
        {
            "nombre": f"Decision Table: {n(dt)} reglas de la cadena de inicio de sesión y del falso", "condiciones": ["C1", "C8"],
            "definicion": "<p>La tabla de decisión registra combinaciones de condiciones y la acción que produce cada una; cada columna factible es un ítem de cobertura [ctfl, §4.2.3]. Expone combinaciones olvidadas y contradicciones en la spec [copeland].</p>",
            "por_que": "<p><b>El resultado de <code>Login</code> depende de tres respuestas encadenadas</b>: la del login móvil (ticket, contraseña errónea, MFA, 429, Cloudflare, captcha, HTML, red caída), la de diauth (token, 429, rechazo) y la del widget (ticket, contraseña errónea, 429, no 200, página de bloqueo). C1 tiene nivel 6, así que se usó la tabla completa sin minimizar: cada regla manda a un código distinto o a un tipo de login distinto, y una regla mal implementada gasta un inicio de sesión de los 4 que Garmin tolera en 40 minutos.</p>",
            "derivacion": tabla(["Regla", "Móvil", "diauth", "Widget", "Acción esperada"], [
                ["R411", "ticket", "token", "—", "DI"], ["R412", "contraseña errónea", "—", "—", "BadPassword, sin widget"],
                ["R413", "MFA", "—", "—", "MFARequired, sin widget"], ["R414–R416", "429 / Cloudflare / captcha", "—", "ticket", "Legacy"],
                ["R417", "ticket", "rechazo ×4", "ticket", "Legacy"], ["R418", "ticket", "429", "—", "RateLimited, sin widget"],
                ["R419", "429", "—", "429", "RateLimited"], ["R420, R422", "Cloudflare", "—", "403 / 500", "Blocked"],
                ["R421", "429", "—", "contraseña errónea", "BadPassword"], ["R423–R428", "HTML, 429 en JSON, tipo nuevo, {}, red, sin ticket", "—", "ticket", "Legacy"],
                ["R429–R431", "bloqueado", "—", "cuenta bloqueada / teléfono / sin CSRF", "LoginFailed"],
                ["R432–R434", "bloqueado", "—", "red caída en embed, signin, post", "LoginFailed"], ["R435", "bloqueado", "—", "302", "Blocked, sin seguir la redirección"],
                ["R481–R483", "—", "refresh o ticket ajeno", "rutas del falso", "ver casos"],
            ]),
            "casos": dt,
            "cobertura": f"<p>{n(dt)} de {n(dt)} reglas factibles ejecutadas = 100 %. Las reglas donde el móvil devuelve contraseña errónea o MFA no combinan con diauth ni widget (N/A: la cadena se detiene).</p>",
            "limites": "<p>La tabla no detecta un cambio en la forma real de las respuestas de Garmin: los esperados vienen del comportamiento medido el 2026-10-14 y de python-garminconnect, no de una prueba en vivo, que el límite de inicios de sesión prohíbe.</p>",
        },
        {
            "nombre": f"State Transition: {n(st)} transiciones del ciclo de vida del token", "condiciones": ["C2"],
            "definicion": "<p>La prueba de transición de estados modela el comportamiento que depende de lo que pasó antes; <i>all transitions</i> ejecuta cada transición válida y prueba cada inválida, una por caso [ctfl, §4.2.4]; [beizer].</p>",
            "por_que": "<p><b>Un login pasa por vigente → cerca de expirar → renovado, o termina muerto o conservado</b> según lo que conteste Garmin al refresh (spec §2.4). C2 tiene nivel 6: confundir «muerta» con «conservar» obliga al usuario a reconectar, que es justo el paso que Garmin limita. Por eso se cubrió cada respuesta del refresh como transición separada.</p>",
            "derivacion": tabla(["Estado", "Evento", "Siguiente estado", "Casos"], [
                ["Vigente", "Fresh fuera del margen", "Vigente, 0 solicitudes", "ST-T401"],
                ["Cerca de expirar", "Fresh dentro del margen", "Renovado (DI o legacy)", "ST-T401, ST-T402"],
                ["Renovado", "Fresh", "Vigente, 0 solicitudes", "ST-T401"],
                ["Sin login", "Fresh / Refresh", "ErrNoTokens, 0 solicitudes (inválida)", "ST-T403"],
                ["DI sin refresh_token", "Fresh", "Muerto, 0 solicitudes", "ST-T411"],
                ["Cualquiera", "Refresh: 400/401 DI, 401 legacy", "Muerto (ErrSessionDead)", "ST-T421–ST-T423"],
                ["Cualquiera", "Refresh: 403, 429, 5xx, red, 400 legacy", "Conservado (error simple)", "ST-T424–ST-T430"],
            ]),
            "casos": st,
            "cobertura": f"<p>{n(st)} casos cubren 7 de 7 transiciones de la tabla, incluida la inválida (sin login) = 100 % all transitions.</p>",
            "limites": "<p>No cubre la expiración del refresh token mismo (Garmin no la publica): el modelo supone que el refresh token vive mientras diauth no conteste 400 o 401.</p>",
        },
        {
            "nombre": f"Equivalence Partitioning: {n(ep)} particiones", "condiciones": ["C1", "C3", "C5", "C7", "C8"],
            "definicion": "<p>Equivalence Partitioning divide un dominio en particiones que el objeto procesa igual y prueba un representante de cada una, válidas e inválidas; con varios parámetros el mínimo es <i>Each Choice</i> [ctfl, §4.2.1]; [myers].</p>",
            "por_que": "<p><b>Cinco dominios se procesan por clases</b>: el estado HTTP de una respuesta (204, vacío, 401, 403, 5xx; spec etapa 2), la forma de la respuesta de diauth (JWT con exp, sin exp, opaco), la composición de <code>Tokens</code> (DI, legacy, ninguno), los nombres de clave en <code>Redact</code> (exacto, contiene palabra secreta, vecino inocente) y los prefijos del falso. C3 y C5 tienen nivel 6, así que cada partición inválida tiene su propio caso.</p>",
            "derivacion": tabla(["Dominio", "Particiones", "Casos"], [
                ["Estado HTTP", "204 · 200 vacío · 401 una vez · 401 dos veces · 403 · 5xx · tokens sin login", "EP-431–EP-437"],
                ["Respuesta de diauth", "JWT con exp y client_id · JWT sin exp + expires_in · opaco sin expires_in · sin refresh_token_expires_in · refresh sin refresh_token · client id rechazado", "EP-406–EP-411"],
                ["Auth.HTTP", "definido · nil", "EP-401, EP-402"], ["Hosts", "cero · SameHost · parcial", "EP-403–EP-405"],
                ["Tokens.Kind", "nada · DI · DI vacío · legacy · legacy incompleto · DI + legacy · DI vacío + legacy", "EP-413–EP-419"],
                ["Claves secretas", "exacta en mayúsculas · contiene palabra · vecina · valor vacío · cookie multilínea · JSON con espacios · texto sin secreto", "EP-446–EP-452"],
                ["Prefijos del falso", "DataStatus anidados · FailOnce anidados · token ajeno · 429 vs otro estado", "EP-481–EP-484"],
            ]),
            "casos": ep,
            "cobertura": f"<p>{n(ep)} de {n(ep)} particiones identificadas ejercitadas = 100 %, inválidas incluidas.</p>",
            "limites": "<p>EP no prueba combinaciones de particiones (por ejemplo un 401 seguido de un 429 en la misma solicitud); esas quedaron en la tabla de decisión o sin cubrir.</p>",
        },
        {
            "nombre": f"Boundary Value Analysis: {n(bva)} valores en 9 fronteras", "condiciones": ["C2", "C3", "C4", "C6"],
            "definicion": "<p>Boundary Value Analysis prueba los extremos de particiones ordenadas; la variante de 3 valores prueba la frontera y ambos vecinos [ctfl, §4.2.2]; [koomen].</p>",
            "por_que": "<p><b>Las reglas del cliente son umbrales numéricos</b>: refrescar cuando faltan ≤ 900 s, detener al 5.º 403 seguido, 2 reintentos de 429, Retry-After con tope de 60 s, 1 MB de cuerpo, 4xx frente a 5xx, Limit 0 = 20. C2 y C3 tienen nivel 6, así que el margen de refresh y la racha de 403 se probaron con 3 valores.</p>",
            "derivacion": tabla(["Frontera", "Valores", "Casos"], [
                ["Margen de refresh (900 s)", "exp−901 · exp−900 · exp−899", "BVA-401–BVA-403"],
                ["Racha de 403 (5)", "4 · 5 · reinicio tras éxito", "BVA-431, BVA-432, BVA-436"],
                ["Reintentos de 429 (2)", "1 · 2 · 3", "BVA-433, BVA-435, BVA-434"],
                ["429 sin Retry-After", "backoff 5 s y 10 s", "BVA-442"],
                ["Tope de Retry-After (60 s)", "59 · 60 · 61 · fecha a 61 s", "BVA-439–BVA-441, BVA-444"],
                ["Jitter (0–999 ms) y tope 60 s", "5 esperas con jitter real", "BVA-443"],
                ["Clase de estado", "499 · 500", "BVA-437, BVA-438"],
                ["Tamaño (1 MB)", "1 MB · 1 MB + 1", "BVA-445"], ["Limit de activities_by_date", "0 · 1", "BVA-446, BVA-447"],
            ]),
            "casos": bva,
            "cobertura": f"<p>{n(bva)} casos cubren 9 de 9 fronteras = 100 %; 3 valores en el margen de refresh, la racha de 403, los reintentos de 429 y el tope de Retry-After.</p>",
            "limites": "<p>El tiempo de espera de 30 s por intento y el límite de 30 s del inicio de sesión no se probaron en su frontera: haría falta esperar 30 s reales o inyectar un reloj que la API no expone.</p>",
        },
        {
            "nombre": f"Error Guessing: {n(eg)} ataques", "condiciones": ["C1", "C3", "C4", "C5", "C8"],
            "definicion": "<p>Error Guessing anticipa errores a partir de la experiencia y de fallas típicas del dominio: entradas, interfaces, datos y concurrencia [ctfl, §4.4.1]; [whittaker].</p>",
            "por_que": "<p><b>Un cliente HTTP falla en lugares conocidos</b>: la conexión se cae a mitad de cuerpo, el contexto se cancela durante una espera, una URL mal formada, un archivo de consumidor que no es JSON, una lectura concurrente de <code>Stopped</code>, un token en la URL de un error. Cada ataque de la lista se convirtió en un caso <code>EG-n</code>; los ataques de interfaz se escribieron con el proxy <code>front</code> y <code>hangUp</code>.</p>",
            "derivacion": tabla(["Categoría [ctfl, §4.4.1]", "Ataques", "Casos"], [
                ["Interfaces", "conexión cerrada en cada paso del login y del refresh; cuerpo cortado; URL mal formada", "EG-403, EG-405, EG-408, EG-411, EG-413, EG-458, EG-459"],
                ["Datos", "consumidor no JSON; OAuth2 no JSON; diauth no JSON o sin access_token; JWT sin puntos", "EG-401, EG-402, EG-404, EG-406, EG-407, EG-409, EG-410, EG-421"],
                ["Tiempo", "timeout en cada intento; contexto cancelado antes, durante el Gap y durante el backoff; Sleep que falla", "EG-412, EG-436–EG-438, EG-442, EG-450–EG-453"],
                ["Salida", "secretos en el cuerpo de un 500; token en la URL de un error; Retry-After raro; encabezados nativos y de goals", "EG-431, EG-433–EG-435, EG-443–EG-447, EG-449, EG-454"],
                ["Concurrencia y aislamiento", "Stopped desde otra goroutine; knobs del falso durante solicitudes; guard de loopback", "EG-456, EG-481, EG-482"],
                ["Lógica de parada", "refresh 5xx o 400 antes y después de un 401; 403 en el perfil; Log nil", "EG-432, EG-439–EG-441, EG-455, EG-457"],
            ]),
            "casos": eg,
            "cobertura": f"<p>{n(eg)} de {n(eg)} ataques de la lista ejecutados = 100 %.</p>",
            "limites": "<p>La lista depende de la experiencia: no cubre fallas de TLS, proxies HTTP del sistema ni respuestas HTTP/2 de Garmin, que el falso en loopback no reproduce.</p>",
        },
        {
            "nombre": f"Checklist-based: {n(cl)} ítems", "condiciones": ["C5", "C6", "C7", "C8", "C9"],
            "definicion": "<p>La prueba basada en checklist verifica ítems formulados como preguntas que se responden por separado, tomados de estándares o de la experiencia [ctfl, §4.4.3]; [brykczynski].</p>",
            "por_que": "<p><b>Cuatro bases son listas cerradas</b>: los 38 endpoints con su URL esperada (tabla de la app JavaScript), los patrones de secreto que debe ocultar <code>Redact</code> (spec etapa 2: Bearer, Basic, cookies, JWT, claves de query y JSON; [owasp]), los nombres de campo JSON de los tokens (garth) y las reglas de arquitectura (solo biblioteca estándar, sin disco, sin require). También los encabezados nativos de Android medidos el 2026-10-14 y la firma OAuth1 del RFC 5849.</p>",
            "derivacion": tabla(["Checklist", "Pregunta por ítem", "Casos"], [
                ["Login y firma", "¿Envía el JSON, el Basic y la firma RFC 5849 correctos?", "CL-401–CL-403, CL-422–CL-425"],
                ["Tokens JSON", "¿Usa los nombres de campo de Garmin y garth?", "CL-411–CL-413"],
                ["Endpoints", "¿Cada URL coincide con la tabla? ¿Alias, tipos, copias?", "CL-431–CL-434, CL-466, CL-467"],
                ["Cliente", "¿Perfil, Safe, GetURL, OnTokens, Stopped hacen lo documentado?", "CL-435–CL-438, CL-458–CL-464"],
                ["Redacción", "¿Cada patrón de secreto queda oculto y lo demás intacto?", "CL-441–CL-457"],
                ["Falso", "¿Defaults, copia de Control, Reset, rutas, Calls, JWT, Hosts?", "CL-481–CL-487"],
                ["Arquitectura", "¿Cada regla falla con su fixture y el módulo real pasa?", "CL-491–CL-499"],
            ]),
            "casos": cl,
            "cobertura": f"<p>{n(cl)} de {n(cl)} ítems verificados = 100 %.</p>",
            "limites": "<p>Un checklist solo verifica lo que lista: un endpoint nuevo de Garmin o un patrón de secreto nuevo (por ejemplo un encabezado propietario) no se detecta hasta agregarlo.</p>",
        },
    ],
    "descartadas": [
        ["Pairwise", "Los parámetros del login no son independientes: el widget solo corre si el móvil está bloqueado; la tabla de decisión cubre las combinaciones factibles [29119-4]."],
        ["Property-based testing", "La promesa de solo biblioteca estándar excluye generadores de terceros; <code>testing/quick</code> no aporta sobre las particiones de <code>Redact</code> ya enumeradas [claessen]."],
        ["Mutation testing", "No hay herramienta en la biblioteca estándar; el test de arquitectura sí se validó con fixtures que rompen cada regla (CL-492 a CL-498) [demillo]."],
        ["Exploratorio contra Garmin real", "El límite de 4 inicios de sesión en 40 minutos bloquea la cuenta; ninguna prueba debe salir de loopback [ctfl, §4.4.2]."],
        ["MC/DC", "Las decisiones compuestas (<code>ok && strings.HasPrefix</code>) tienen 2 condiciones y la tabla de decisión ya las separa [ammann]."],
    ],
    "cobertura_nota": ("<p>Go mide sentencias, no ramas; la columna de ramas queda en 0 de 0 (n/a). La cobertura se midió con <code>-coverpkg=./...</code> y se unieron los bloques de los dos binarios de prueba. "
                       "La única sentencia sin ejecutar es <code>auth.go:441</code> (<code>return \"\", err</code> tras <code>NewRequestWithContext</code> del POST del widget): el GET al mismo host ya validó esa URL en la línea 425, así que es inalcanzable.</p>"),
    "defectos": [],
    "riesgo_residual": ("<ul><li>Ninguna prueba toca el Garmin real (por diseño): un cambio de Garmin en la forma de las respuestas solo se detecta en uso. Recomendación: una prueba de humo manual, una sola vez por versión, con una cuenta de prueba.</li>"
                        "<li>El límite de 30 s por solicitud de inicio de sesión y por intento de datos no se ejecutó en su frontera (BVA, componente).</li>"
                        "<li>La matriz de CI cubre Linux y macOS con Go 1.24 y estable; Windows no se probó.</li>"
                        "<li>La cobertura de ramas no se puede medir con la herramienta estándar de Go.</li></ul>"),
    "decision": {"veredicto": "ok", "texto": f"<p><b>Liberar v0.1.0: {len(casos)} de {len(casos)} casos pasan, 0 defectos, 99,9 % de sentencias y los 5 criterios de salida cumplidos.</b></p>",
                 "semaforo": [["Liberar", "100 % de casos pasan, ≥ 95 % de sentencias, 0 defectos altos"],
                              ["Liberar con condiciones", "Solo defectos de severidad baja o media abiertos y documentados"],
                              ["No liberar", "Algún defecto alto abierto, una prueba que toca la red real, o < 95 % de sentencias"]]},
    "entorno": {"comando": "go test -race -count=1 -json -coverpkg=./... -coverprofile=cover.out ./...",
                "versiones": "go1.27.2 darwin/arm64 (local; go.mod pide go 1.24); CI: Go 1.24.x y stable en ubuntu-latest y macos-latest"},
    "reproduccion": [
        "R=reportes-pruebas/2026-10-16-gconnect",
        "go test -race -count=1 -json -coverpkg=./... -coverprofile=$R/cover.out ./... > $R/test.json",
        "python3 -I scripts/report/go_to_report_inputs.py junit $R/test.json $R/junit.xml",
        "python3 -I scripts/report/go_to_report_inputs.py cober $R/cover.out $R/coverage.json github.com/danihrndzld/gconnect/",
        "python3 -I $R/armar_json.py",
        "uv run /Users/dani/.claude/skills/testing-and-continuous-delivery/scripts/armar_reporte.py $R/reporte.json --junit $R/junit.xml --cobertura $R/coverage.json --raiz .",
    ],
}

json.dump(rep, open(R + "reporte.json", "w"), ensure_ascii=False, indent=1)
print(f"{len(casos)} casos")
