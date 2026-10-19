# E36731A LAN Control v0.2.0

Windows 本機 HTML 控制介面：透過 LAN / TCP 5025 傳送 SCPI，不需安裝 Python、Go、PyVISA 或 Keysight VISA。

**目前為未完成實機驗證的版本：Windows x64 EXE 已交叉編譯，27 項替身通訊／控制測試及競態檢查通過；尚未在 Windows 執行、未完成瀏覽器視覺驗證，也未連接 E36731A 實機。**

## 下載與使用

[下載 Windows 64 位元免安裝包](https://github.com/Oloid666/E36731A-LAN-Control/raw/refs/heads/main/downloads/E36731A-LAN-Windows-x64.zip)

1. 解壓縮 ZIP，雙擊 `E36731A-LAN-Control.exe`。
2. 保留程式視窗；瀏覽器會自動開啟。若沒有開啟，複製視窗顯示的完整本機網址到瀏覽器。
3. 電腦與儀器接至可互通的 LAN，在儀器面板 LAN 設定頁查看 IPv4 位址。
4. 在網頁輸入儀器 IP，按「連線」。必須允許連線到儀器 TCP 5025。
5. 先確認輸出 OFF，再設定 Source/Sink、CC/CV/CR 與相關參數，按「套用並讀回驗證」。
6. 依原廠操作規範核對接線與負載，才按 ON；結束使用「OFF 並結束程式」。

適用 Windows 10/11 x64；EXE 未簽章，公司可能需要 IT 審核。程式只監聽 `127.0.0.1` 的隨機埠，HTML 已內嵌，不需另行開啟 HTML。它不是可直接由 GitHub Pages 控制內網儀器的網站。

完整說明：[使用說明.txt](使用說明.txt)。下載包 SHA-256 見 [SHA256SUMS.txt](SHA256SUMS.txt)。

## 功能範圍

- Source：電壓與電流限制。**實際 CC/CV 由負載決定**；選單設定的是 ON/OFF 轉換偏好，不能強制任意負載進入 CC 或 CV。
- Sink CC：設定吸收電流，電壓由外部電源決定。
- Sink CV：設定電壓與吸收電流限制。
- 型號識別、設定讀回、輸出 ON/OFF、實測電壓／電流與計算功率 V×I、SCPI 紀錄與最近 120 筆 CSV 匯出。
- Sink CR：顯示與修改電子負載設定阻值（0.08–4000 Ω），只有 CR 模式會依 I=V/R 吸收電流。
- 不包含斜率、電池 ESR、SOC、容量或電池模型。

本版採 30 V／20 A／200 W 的應用限制，不代表儀器完整規格。Sink 超限停止是軟體輪詢，不能取代硬體保護。V/I 是循序量測，功率由 V×I 計算，不等同儀器的平均功率，也不是同步高速擷取。

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

主要指令：`EMUL PSUP` / `EMUL LOAD`、`FUNC CURR` / `FUNC VOLT`（Sink）、`FUNC RES` / `RES` / `RES?`（Sink CR）、`VOLT`、`CURR`、`CURR:LIM`（Sink CV）、`OUTP`、`MEAS:*?`、`STAT:OPER:COND?`、`SYST:ERR?`。切換 `EMUL` 模式會重設儀器。

## v0.2.0 修正

使用者的 K-01.14.03-01.00-02.14-01.06 韌體已回覆 `*IDN?`、`MEAS:VOLT?`、`MEAS:CURR?`，但 `MEAS:POW?` 在原程式 2 秒期限內沒有回覆。移除此查詢，改顯示與匯出 **計算功率 V×I**。這只能確認目前找到的逾時點，其他控制功能仍需實機驗證。

## CR 與電池 ESR 的區別

本版 CR 是電子負載輸入阻值，並非電池等效串聯內阻。不能用本版設定 10 mΩ 電池 ESR，不能把 PSUP/LOAD 手動切換當成即時雙向電池模擬，也沒有透過 PC 輪詢實作高速 ESR 回授。ESR 可程式設定與反應速度仍待核對原廠能力與實機韌體。

CR 設定使用 `FUNC RES`、`RES:MODE FIX`、同訊息 `RES:RANG …;:RES …`，並以 `RES?` 讀回。量程依手冊第 105 頁選擇 0.08–30 Ω、10–1250 Ω、100–4000 Ω。ON 前依目前輸入電壓估算 I=V/R、P=V²/R，超過本版限制則拒絕 ON；後續超限停止仍是軟體輪詢，不能取代硬體保護。
