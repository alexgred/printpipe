# Что прислать, если на macOS что-то сломалось

К плану `docs/macos-test-plan.md`. Присылай файлами или вставляй текст как есть. Перед отправкой проверь, что в конфиге и логах нет ничего лишнего: printpipe хранит в них только origin, порт и очередь, но лучше убедиться самому.

## Всегда

1. **Шаг плана**: номер раздела и пункта из `docs/macos-test-plan.md` и что ты сделал.
2. **Полный вывод команды**: от команды до конца, без сокращений, плюс код выхода (`echo $?` сразу после неё).
3. **Чего ты ждал и что произошло на деле.** Например: «этикетка не вышла», «вышла с мусором». Если с этикеткой что-то не так, сфотографируй её.
4. **Окружение**:
   ```sh
   sw_vers; uname -m; printpipe version
   ```

## Для диагностики

5. `printpipe status`: показывает, где лежат все файлы и что запущено.
6. Лог агента: `~/Library/Logs/printpipe.log` целиком (и `printpipe.log.1`, если он есть).
7. Конфиг: `~/Library/Application Support/printpipe/config.json`.
8. Сырой вывод из раздела 1 плана: `ioreg.txt`, `lpinfo.txt`, `lpstat.txt`, `launchctl.txt`.

## В зависимости от проблемы

### Принтер не найден или не создалась очередь

```sh
ioreg -r -c IOUSBHostDevice -l -w 0 > ioreg.txt
sudo lpinfo --include-schemes usb -v > lpinfo.txt
lpstat -e
lpoptions -p XP-365B
```

### Проблемы с запуском, остановкой или автозапуском

```sh
launchctl print gui/$UID/io.github.alexgred.printpipe
cat ~/Library/Application\ Support/printpipe/io.github.alexgred.printpipe.plist
cat ~/Library/LaunchAgents/io.github.alexgred.printpipe.plist
curl -s http://127.0.0.1:8631/health
lsof -nP -iTCP:8631
```

### Печать уходит, но этикетка не выходит

```sh
lpstat -W all -o
sudo tail -n 200 /var/log/cups/error_log
```

### macOS не даёт запустить бинарник

```sh
xattr -l ~/.local/bin/printpipe
codesign -dv ~/.local/bin/printpipe
```

Добавь точный текст системного окна или его скриншот.
