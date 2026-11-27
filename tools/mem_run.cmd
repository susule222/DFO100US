@echo off
cd /d "D:\DNF project\service\美服100服务端\server-us100"
"C:\Users\Li_P\AppData\Local\Temp\equipmentbuild.exe" -source "D:\DNF project\service\美服100服务端\runtime\pvf_source\Script.inner.pvf" -output "C:\Users\Li_P\AppData\Local\Temp\eq_test.json" -max-rarity 1
echo EQ_EXIT=%errorlevel%
