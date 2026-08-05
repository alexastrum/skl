

# skl: Gestor CLI de Habilidades para Agentes

`skl` es una herramienta CLI rápida y sin dependencias para gestionar Habilidades de Agentes, escrita en Go.

Este proyecto es un port y reescritura directa en Go del paquete original [`skills`](https://www.npmjs.com/package/skills) (comúnmente ejecutado mediante `npx skills`) de TypeScript. Replica su conjunto completo de comandos y lógica, sirviendo como un reemplazo de alto rendimiento y seguro, inmune a los ataques en la cadena de suministro de NPM.

## ¿Por qué Go?

TypeScript es excelente en el navegador, pero Node.js es un lastre para las herramientas CLI. Obtienes dependencias infladas, tiempos de inicio lentos y dolores de cabeza con la cadena de suministro.

Habría podido usar Rust, pero parecía exagerado. Rust es genial para núcleos de alta concurrencia o motores críticos de seguridad, pero luchar contra el verificador de préstamos (borrow checker) y gestionar las anotaciones de vida útil (lifetimes) añade demasiada fricción para una herramienta de enlaces simbólicos sencilla. Go encuentra el equilibrio perfecto: compila en menos de un segundo, tiene una biblioteca estándar rica y me permite escribir código sincrónico y directo sin importar la mitad de crates.io.

### Cero dependencias
La CLI original en TypeScript importaba 127 paquetes solo para manejar tareas básicas de archivos y networking. La versión en Go no usa ninguna. La escribí por completo utilizando la biblioteca estándar de Go, importando solo `yaml.v3` para parsear el frontmatter.

### Una huella mucho más pequeña
La diferencia en el tamaño físico es ridícula:
*   TypeScript original: 10 108 líneas de código repartidas en 33 archivos, más una carpeta `node_modules` de 116 MB.
*   Reescritura en Go (`skl`): 2 864 líneas de código repartidas en 11 archivos, compilándose a un binario estático de 9,2 MB.

Intercambiar las rutinas de arranque de V8, los envoltorios de tareas asíncronas y las herramientas de compilación por código Go sincrónico y plano redujo mis líneas de código en un 71%.

### Inicio instantáneo
Iniciar V8, las importaciones y el análisis de JS hace que las herramientas de Node se sientan lentas. Go es un único binario compilado que arranca casi al instante:
*   `time node skills-ts version` -> 100ms
*   `time skl version` -> 2ms

### Manejo real de errores
Go te obliga a tratar los errores como valores, no como excepciones. Empaquetarlos en los límites de los paquetes y manejarlos explícitamente hace que la CLI sea increíblemente predecible.

## Instalación y uso

### Instalación global
Instala la herramienta CLI globalmente:
```bash
go install github.com/alexastrum/skl@latest
```

Para repositorios privados, configura tu entorno de shell:
```bash
export GOPRIVATE=github.com/alexastrum/*
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

### Compilar desde el código fuente
```bash
# Clonar el repositorio
git clone https://github.com/alexastrum/skl.git
cd skl

# Compilar el binario
go build -o skl

# Ejecutar pruebas
go test -v ./...
```

### Ejecutar binarios de lanzamiento sin firmar
Si instalas `skl` usando `go install github.com/alexastrum/skl@latest` o lo compilas desde el código fuente, el binario se construirá nativamente en tu sistema y se ejecutará sin problemas. 

Sin embargo, si descargas los ejecutables de lanzamiento precompilados desde GitHub, estarán sin firmar. Dependiendo de tu sistema operativo, los filtros de seguridad pueden evitar que se lancen.

#### macOS (Cuarentena de Gatekeeper)
Cuando descargas e intentas ejecutar el binario sin firma para macOS, Apple Gatekeeper puede poner el archivo en cuarentena y mostrar un error ("'skl-darwin-arm64' está dañado y no se puede abrir" o "el desarrollador no se puede verificar").
* **Omisión por línea de comandos**: Para eliminar el atributo de cuarentena, ejecuta el siguiente comando en tu terminal:
  ```bash
  xattr -d com.apple.quarantine ./skl-darwin-arm64
  ```
* **Omisión desde Configuración del sistema**: Alternativamente, navega a **Configuración del sistema > Privacidad y seguridad**, desplázate hasta la sección de Seguridad y haz clic en **Permitir de todos modos** junto a la advertencia para `skl`.

#### Windows (Filtro SmartScreen)
Al ejecutar el ejecutable sin firma para Windows (`skl-windows-amd64.exe`), Windows Defender SmartScreen puede mostrar una advertencia ("Windows protegiste tu PC").
* **Omisión manual**: Haz clic en **Más información** dentro del cuadro de diálogo de SmartScreen y, a continuación, haz clic en **Ejecutar de todos modos**.
* **Omisión con PowerShell**: Alternativamente, desbloquea el archivo usando PowerShell:
  ```powershell
  Unblock-File -Path .\skl-windows-amd64.exe
  ```


## Comandos de la CLI

```bash
skl - Agent Skills CLI Manager

Usage:
  skl <command> [arguments]

Commands:
  init [name]        Initialize a new agent skill template in Cwd or a subfolder.
  add <source>       Install a local skill folder to project or global scope.
  list               List installed skills for detected agents.
  remove <names...>  Uninstall and delete skill configurations.
  find <query>       Search/retrieve an installed skill by name or slug.
  update <name> <src> Refresh an installed skill from its source path.
  version            Print CLI tool version.

Flags:
  -g, --global       Apply action to global scope (home directory).
  -y, --yes          Skip confirmation prompts (automatic non-interactive).
  --copy             Force copy instead of symlink.
  -a, --agent <name> Filter action by agent type.
  -v, --verbose      Log all persistent file system changes.
```

### 1. Inicializar una habilidad (`init`)
Crea una plantilla `SKILL.md` con frontmatter en YAML:
```bash
skl init my-new-skill
```

### 2. Agregar habilidades (`add`)
Instala habilidades locales o remotas en agentes activos:
```bash
skl add ./my-skill -v
skl add samber/cc-skills-golang -g --copy
```

### 3. Listar habilidades instaladas (`list`)
Escanea las carpetas de los agentes y muestra un resumen:
```bash
skl list
```

### 4. Eliminar una habilidad (`remove`)
Desinstala y elimina las habilidades de forma segura:
```bash
skl remove my-skill
```

### 5. Buscar una habilidad instalada (`find`)
Busca una habilidad instalada por su nombre:
```bash
skl find golang-testing
```

## Características
`skl` es un port completo de características de la CLI original en TypeScript:
*   **Fuentes flexibles**: Instala habilidades desde carpetas locales, URLs completas de GitHub/GitLab o abreviaturas (`owner/repo`).
*   **Enlazado sin sobrecarga**: Crea enlaces simbólicos de las habilidades en los ámbitos activos de los agentes de forma predeterminada, creando una única fuente de verdad para actualizaciones fáciles (o usa `--copy` para escribir copias físicas independientes).
*   **Seguridad integral**: Impone protección contra el desplazamiento de directorios y saneamiento de nombres de archivos en cada paso.
*   **Ámbitos flexibles**: Dirige a proyectos individuales o actúa como un registro global a nivel de sistema con `-g`.

## Agentes compatibles (51+)
`skl` detecta e instala automáticamente en cualquiera de estos asistentes y editores de desarrollo desde el primer momento:

<details>
<summary><b>Haz clic para expandir la lista completa de los 51 agentes compatibles</b></summary>

| Agente | Clave `--agent` | Carpeta del proyecto | Carpeta global |
| :--- | :--- | :--- | :--- |
| **AiderDesk** | `aider-desk` | `.aider-desk/skills/` | `~/.aider-desk/skills/` |
| **Antigravity** | `antigravity` | `.agents/skills/` | `~/.gemini/antigravity/skills/` |
| **Augment** | `augment` | `.augment/skills/` | `~/.augment/skills/` |
| **IBM Bob** | `bob` | `.bob/skills/` | `~/.bob/skills/` |
| **Claude Code** | `claude-code` | `.claude/skills/` | `~/.claude/skills/` |
| **OpenClaw** | `openclaw` | `skills/` | `~/.openclaw/skills/` |
| **CodeArts Agent** | `codearts-agent` | `.codeartsdoer/skills/` | `~/.codeartsdoer/skills/` |
| **CodeBuddy** | `codebuddy` | `.codebuddy/skills/` | `~/.codebuddy/skills/` |
| **Codemaker** | `codemaker` | `.codemaker/skills/` | `~/.codemaker/skills/` |
| **Code Studio** | `codestudio` | `.codestudio/skills/` | `~/.codestudio/skills/` |
| **Codex** | `codex` | `.agents/skills/` | `~/.codex/skills/` |
| **Command Code** | `command-code` | `.commandcode/skills/` | `~/.commandcode/skills/` |
| **Continue** | `continue` | `.continue/skills/` | `~/.continue/skills/` |
| **Cortex Code** | `cortex` | `.cortex/skills/` | `~/.snowflake/cortex/skills/` |
| **Crush** | `crush` | `.crush/skills/` | `~/.config/crush/skills/` |
| **Cursor** | `cursor` | `.agents/skills/` | `~/.cursor/skills/` |
| **Deep Agents** | `deepagents` | `.agents/skills/` | `~/.deepagents/agent/skills/` |
| **Devin for Terminal** | `devin` | `.devin/skills/` | `~/.config/devin/skills/` |
| **Droid** | `droid` | `.factory/skills/` | `~/.factory/skills/` |
| **Firebender** | `firebender` | `.agents/skills/` | `~/.firebender/skills/` |
| **ForgeCode** | `forgecode` | `.forge/skills/` | `~/.forge/skills/` |
| **Gemini CLI** | `gemini-cli` | `.agents/skills/` | `~/.gemini/skills/` |
| **GitHub Copilot** | `github-copilot` | `.agents/skills/` | `~/.copilot/skills/` |
| **Goose** | `goose` | `.goose/skills/` | `~/.config/goose/skills/` |
| **Hermes Agent** | `hermes-agent` | `.hermes/skills/` | `~/.hermes/skills/` |
| **Junie** | `junie` | `.junie/skills/` | `~/.junie/skills/` |
| **iFlow CLI** | `iflow-cli` | `.iflow/skills/` | `~/.iflow/skills/` |
| **Kilo Code** | `kilo` | `.kilocode/skills/` | `~/.kilocode/skills/` |
| **Kiro CLI** | `kiro-cli` | `.kiro/skills/` | `~/.kiro/skills/` |
| **Kode** | `kode` | `.kode/skills/` | `~/.kode/skills/` |
| **MCPJam** | `mcpjam` | `.mcpjam/skills/` | `~/.mcpjam/skills/` |
| **Mistral Vibe** | `mistral-vibe` | `.vibe/skills/` | `~/.vibe/skills/` |
| **Mux** | `mux` | `.mux/skills/` | `~/.mux/skills/` |
| **OpenCode** | `opencode` | `.agents/skills/` | `~/.config/opencode/skills/` |
| **OpenHands** | `openhands` | `.openhands/skills/` | `~/.openhands/skills/` |
| **Pi** | `pi` | `.pi/skills/` | `~/.pi/agent/skills/` |
| **Qoder** | `qoder` | `.qoder/skills/` | `~/.qoder/skills/` |
| **Qwen Code** | `qwen-code` | `.qwen/skills/` | `~/.qwen/skills/` |
| **Rovo Dev** | `rovodev` | `.rovodev/skills/` | `~/.rovodev/skills/` |
| **Roo Code** | `roo` | `.roo/skills/` | `~/.roo/skills/` |
| **Tabnine CLI** | `tabnine-cli` | `.tabnine/agent/skills/` | `~/.tabnine/agent/skills/` |
| **Trae / Trae CN** | `trae` / `trae-cn` | `.trae/skills/` | `~/.trae/skills/` / `~/.trae-cn/skills/` |
| **Windsurf** | `windsurf` | `.windsurf/skills/` | `~/.codeium/windsurf/skills/` |
| **Zencoder** | `zencoder` | `.zencoder/skills/` | `~/.zencoder/skills/` |
| **Neovate** | `neovate` | `.neovate/skills/` | `~/.neovate/skills/` |
| **Pochi** | `pochi` | `.pochi/skills/` | `~/.pochi/skills/` |
| **AdaL** | `adal` | `.adal/skills/` | `~/.adal/skills/` |
| **Universal / Replit** | `universal` / `replit` | `.agents/skills/` | `~/.config/agents/skills/` |

</details>

## Arquitectura y seguridad

### Saneamiento de rutas
Cada nombre de habilidad de entrada se sanea utilizando `SanitizeName()`. Esto reemplaza los caracteres especiales con guiones, convierte la cadena a minúsculas y la trunca a 255 caracteres para evitar intentos de desplazamiento de directorios (CWE-22).

### Deduplicación de habilidades compartidas
Para ahorrar espacio en disco y evitar clonar repositorios de git idénticos múltiples veces, las habilidades se descargan una sola vez a un directorio compartido (`.agents/skills/<skill-name>`) y luego se enlazan simbólicamente en el ámbito activo de cada agente. Cuando eliminas una habilidad de un agente específico, la CLI no elimina ciegamente los archivos fuente compartidos. En su lugar, verifica las referencias activas en todos los agentes configurados del espacio de trabajo, eliminando los archivos físicos del repositorio solo cuando el último agente deja de usarlos. Esto evita romper otros agentes activos que aún dependen de esos enlaces simbólicos.

### Autodetección de agentes
No necesitas configurar manualmente qué agentes estás ejecutando. Cuando agregas, listás o actualizas habilidades, `skl` rastrea automáticamente las ubicaciones estándar para detectar entornos activos:
*   **Detección a nivel de proyecto**: Escanea el directorio de trabajo actual en busca de carpetas de agentes activos (como `.cursor/`, `.claude/`, `.cline/` o `.zed/`). Si están presentes, esos agentes son objetivo de las acciones del espacio de trabajo local.
*   **Detección de entorno global**: Aprovecha rutas estándar específicas de la plataforma (como `~/.config`, `$XDG_CONFIG_HOME` o `%APPDATA%` en Windows) para descubrir instalaciones activas de editores y asistentes de desarrollo.

### Autodetección de ámbito
Al ejecutarse de forma no interactiva con `-y` o `--yes`, la CLI resuelve automáticamente el ámbito de instalación:
*   Si tu directorio de trabajo actual contiene un proyecto activo o carpetas de agentes inicializadas, instala en el **ámbito del proyecto** (`./.agents/skills/`).
*   Si no se detecta un entorno de proyecto local, retrocede al **ámbito global** (`~/.gemini/antigravity/skills/` o equivalente).
