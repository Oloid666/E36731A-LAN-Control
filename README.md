# E36731A LAN Control

Windows 本機 HTML 控制介面：透過 LAN / TCP 5025 傳送 SCPI，不需安裝 Python、Go、PyVISA 或 Keysight VISA。

**目前為未完成實機驗證的版本：Windows x64 EXE 已交叉編譯，20 項替身通訊／控制測試及競態檢查通過；尚未在 Windows 執行、未完成瀏覽器視覺驗證，也未連接 E36731A 實機。**

## 下載與使用

[下載 Windows 64 位元免安裝包](https://github.com/Oloid666/E36731A-LAN-Control/raw/refs/heads/main/downloads/E36731A-LAN-Windows-x64.zip)

1. 解壓縮 ZIP，雙擊 `E36731A-LAN-Control.exe`。
2. 保留程式視窗；瀏覽器會自動開啟。若沒有開啟，複製視窗顯示的完整本機網址到瀏覽器。
3. 電腦與儀器接至可互通的 LAN，在儀器面板 LAN 設定頁查看 IPv4 位址。
4. 在網頁輸入儀器 IP，按「連線」。必須允許連線到儀器 TCP 5025。
5. 先確認輸出 OFF，再設定 Source/Sink、CC/CV、電壓與電流，按「套用並讀回驗證」。
6. 依原廠操作規範核對接線與負載，才按 ON；結束使用「OFF 並結束程式」。

適用 Windows 10/11 x64；EXE 未簽章，公司可能需要 IT 審核。程式只監聽 `127.0.0.1` 的隨機埠，HTML 已內嵌，不需另行開啟 HTML。它不是可直接由 GitHub Pages 控制內網儀器的網站。

完整說明：[使用說明.txt](使用說明.txt)。下載包 SHA-256 見 [SHA256SUMS.txt](SHA256SUMS.txt)。

## 功能範圍

- Source：電壓與電流限制。**實際 CC/CV 由負載決定**；選單設定的是 ON/OFF 轉換偏好，不能強制任意負載進入 CC 或 CV。
- Sink CC：設定吸收電流，電壓由外部電源決定。
- Sink CV：設定電壓與吸收電流限制。
- 型號識別、設定讀回、輸出 ON/OFF、實測電壓／電流／功率、SCPI 紀錄與最近 120 筆 CSV 匯出。
- 不包含斜率、內阻、SOC、容量或電池模型。

本版採 30 V／20 A／200 W 的應用限制，不代表儀器完整規格。Sink 超限停止是軟體輪詢，不能取代硬體保護。V/I/P 是循序量測，不是同步高速擷取。

## 輸出與通訊失效

- 初次連線不自動設定或 ON；套用參數也不會自動 ON。
- 通訊錯誤時盡力傳送 OFF 後斷線，但未取得確認時顯示未知，需檢查實機面板。
- 網頁超過 10 秒未回報，程式會嘗試 OFF 並斷線；背景分頁節流也可能觸發。
- 強制關閉 EXE、斷網、當機或關機時無法保證 OFF。OFF 不代表物理隔離，也不是硬體緊急停止。
- 不自動清除儀器保護或恢復輸出；請勿同時用其他程式改變儀器設定。

## 原始碼與重建（開發者）

原始碼位於 [src](src)。一般使用者不需安裝開發工具。

```sh
cd src
go test -race ./...
go vet ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o E36731A-LAN-Control.exe .
```

程式僅使用 Go 標準函式庫；`index.html` 由 Go embed 內嵌。若修改 HTML，需重新編譯 EXE。

原廠指令依據：[E36731A Programming Guide](https://www.keysight.com/us/en/assets/9923-01316/programming-guides/E36731A-Battery-Emulator-Programming-Guide.pdf)，E36731-90009，Edition 2，August 2026。

主要指令：`EMUL PSUP` / `EMUL LOAD`、`FUNC CURR` / `FUNC VOLT`（Sink）、`VOLT`、`CURR`、`CURR:LIM`（Sink CV）、`OUTP`、`MEAS:*?`、`STAT:OPER:COND?`、`SYST:ERR?`。切換 `EMUL` 模式會重設儀器。
