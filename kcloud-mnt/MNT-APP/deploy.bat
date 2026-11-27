@echo off
cd /d "%~dp0"

echo.
echo ====================================
echo  openKcloud CMP - Netlify deploy
echo ====================================
echo.

if not exist "out\index.html" (
  echo [ERROR] out folder not found. Building first...
  echo.
  call npm run build
  if errorlevel 1 (
    echo.
    echo [ERROR] Build failed. Check the output above.
    pause
    exit /b 1
  )
)

echo.
echo Uploading out\ folder to Netlify...
echo - First run: browser opens for one-time login + new-site setup
echo - Later runs: deploys automatically to the same URL
echo.

call npx --yes netlify-cli deploy --dir=out --prod

echo.
echo ====================================
echo  Done. The "Website URL" above is your deploy URL.
echo ====================================
echo.
pause
