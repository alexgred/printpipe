# План тестирования на macOS

Первый запуск printpipe на настоящем Mac, с Apple Silicon или Intel. Сами шаги описаны в `TESTING.md`. Этот файл выстраивает шаги для macOS по порядку, от самых рискованных, и говорит, что сохранить. Результаты записывай в таблицы `TESTING.md`, как для Windows и Fedora.

Подготовка: обычный пользователь, вошедший в графическую сессию; XP-365B по USB с рулоном 40×30; printpipe не установлен; порт 8631 свободен. Запиши версию macOS, процессор и сборку printpipe.

## 1. Сначала сохрани сырой вывод

printpipe разбирает вывод `ioreg`, `lpinfo` и `launchctl print` по описаниям формата, а не по выводу настоящего Mac. До установки сохрани:

```sh
sw_vers > sw_vers.txt; uname -m >> sw_vers.txt
ioreg -r -c IOUSBHostDevice -l -w 0 > ioreg.txt
sudo lpinfo --include-schemes usb -v > lpinfo.txt
LC_ALL=C.UTF-8 lpstat -r > lpstat.txt; lpstat -e >> lpstat.txt 2>&1; echo "exit $?" >> lpstat.txt
```

Когда агент запустится (раздел 2), добавь ещё:

```sh
launchctl print gui/$UID/io.github.alexgred.printpipe > launchctl.txt
```

Если разбор не сработает, эти файлы станут фикстурами для тестов исправления.

- [ ] Сохранены `ioreg.txt`, `lpinfo.txt`, `lpstat.txt`, `launchctl.txt`
- [ ] `lpstat -r` выводит `scheduler is running`
- [ ] Если очередей нет совсем: записано, что при этом делает `lpstat -e`

## 2. Установка одной строкой

`TESTING.md` → «The one-line install», шаги 5–7, выполненные на Mac. Одна команда проверяет скачивание, поиск принтера, создание очереди, launchd и тестовую этикетку.

```sh
curl -fsSL https://github.com/alexgred/printpipe/releases/latest/download/install.sh | sh -s -- --origin https://app.example
```

- [ ] По строке на каждый шаг, этикетка вышла, кириллица напечатана кириллицей, `echo $?` выводит `0`
- [ ] `sudo` спрашивает пароль в терминале, хотя скрипт пришёл через пайп
- [ ] В `ls ${TMPDIR:-/tmp}` не осталось папки `tmp.*`
- [ ] `printpipe status` показывает агент `running under launchd` из `~/.local/bin` или говорит, как добавить его в `PATH`
- [ ] `xattr ~/.local/bin/printpipe` не показывает `com.apple.quarantine`; `codesign -v ~/.local/bin/printpipe` проходит
- [ ] Без сети: `could not reach GitHub`, код выхода 1

## 3. CUPS

`TESTING.md` → «macOS: through CUPS» и «Linux: creating a CUPS queue».

- [ ] `printer.ListUSB` находит принтер; записать, под каким именем (`Xprinter XP-365B` или `Printer Printer-80`)
- [ ] `lpinfo` называет его `usb://Xprinter/XP-365B`
- [ ] `lpadmin -P` принимает минимальный PPD; в `lpoptions -p XP-365B` есть `device-uri` и `printer-is-shared=false`
- [ ] В `config.json` есть `"queue"` и `"created_queue"`, рядом не осталось файла `.ppd`
- [ ] С `"queue": "no-such-queue"` команда `test-print` выводит `the printer was not found`
- [ ] Повторный `printpipe install`: без пароля, без новой очереди, `Agent restarted`

## 4. launchd

`TESTING.md` → «macOS: running in the background», шаги 1–11. Самые хрупкие места:

- [ ] 1: `start` работает; `plutil -lint` принимает plist; в `launchctl print` есть `pid = N`, совпадающий с `/health`, и `program = <бинарник>`. Повторный `start` выводит `already running`
- [ ] 2: агент продолжает работать после закрытия терминала
- [ ] 3: после `restart` `/health` сразу отвечает с новым `pid`
- [ ] 4: `stop`; повторный `stop` выводит `not running`; `restart` запускает агент
- [ ] 5: plist изменён вручную, затем `restart`: нет ошибки `Bootstrap failed: 5`
- [ ] 6: `startup on`, выйти и войти снова: агент работает; `stop` срабатывает; `startup off`, выйти и войти: агент не работает
- [ ] 7: после `kill -SEGV` launchd перезапускает агент примерно за 10 с; со сломанным конфигом выводится `the agent did not answer within 10 s`
- [ ] 8: `serve` в одном терминале, `stop` в другом останавливает агент
- [ ] 9: порт 8631 занят: `port 8631 is used by another program`, код выхода 1
- [ ] 10: по `ssh` без входа в графическую сессию: `start` передаёт то, что ответил `launchctl`, и ничего не остаётся загруженным
- [ ] 11: копия в `/tmp` (это `/private/tmp`): `serve`, затем `stop` из той же копии; записать, что выводится

## 5. Остальные команды

Каждый раздел `TESTING.md`, на Mac:

- [ ] `install`, шаги 1–11 (шаг 3: `xattr` установленного бинарника)
- [ ] `status`, шаги 1–5, 8, 9
- [ ] `config`, шаги 1–5
- [ ] `upgrade`, шаги 1–4, 6–9 (шаг 7: `KeepAlive` у launchd не держит файл; шаг 9: `codesign -v` и нет quarantine)
- [ ] `remove`, шаги 1–6 (шаг 2: оба plist удалены, `launchctl print` не находит сервис, после повторного входа агента нет)

## Минимум

Если времени мало: разделы 1–3 и шаги 1–6 раздела 4. Это путь, которым пройдёт реальный пользователь.
