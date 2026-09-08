package com.query.analysis;

import javax.crypto.Cipher;
import javax.crypto.spec.IvParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;

public class AESCBCUtil {
    // 和Go一致密钥 7sK9p2R5zG8tB4vN 16字节 AES128
    private static final String AES_KEY_STR = "7sK9p2R5zG8tB4vN";
    private static final int AES_BLOCK_SIZE = 16;
    private static final String TARGET_URL = "http://192.168.50.177:8081/api/v1/exec";

    public static void main(String[] args) {
        try {
            // 请求原始JSON
            String reqJson = "{\"script\": \"echo $1\", \"args\": [\"hello world\"], \"timeout\": 30}";
            System.out.println("原始请求JSON: " + reqJson);

            // AES加密：返回 IV(16) + 密文 byte[]
            byte[] encryptBytes = aesCbcEncrypt(reqJson.getBytes(StandardCharsets.UTF_8), AES_KEY_STR);
            System.out.println("加密后二进制长度 = " + encryptBytes.length);

            // POST 发送二进制body
            byte[] respEncBytes = doPostBinary(TARGET_URL, encryptBytes);
            System.out.println("接口返回二进制密文长度 = " + respEncBytes.length);

            // 解密响应
            byte[] plainRespBytes = aesCbcDecrypt(respEncBytes, AES_KEY_STR);
            String respJson = new String(plainRespBytes, StandardCharsets.UTF_8);
            System.out.println("解密后响应JSON：" + respJson);
        } catch (Exception e) {
            e.printStackTrace();
        }
    }

    /**
     * AES-CBC + PKCS7填充
     * 返回：IV(16字节) + cipherText，和Go完全对齐
     */
    public static byte[] aesCbcEncrypt(byte[] plainData, String keyStr) throws Exception {
        byte[] key = keyStr.getBytes(StandardCharsets.UTF_8);
        SecretKeySpec keySpec = new SecretKeySpec(key, "AES");
        Cipher cipher = Cipher.getInstance("AES/CBC/NoPadding");

        byte[] paddedData = pkcs7Pad(plainData, AES_BLOCK_SIZE);

        // 随机IV
        byte[] iv = new byte[AES_BLOCK_SIZE];
        SecureRandom random = new SecureRandom();
        random.nextBytes(iv);
        IvParameterSpec ivSpec = new IvParameterSpec(iv);

        cipher.init(Cipher.ENCRYPT_MODE, keySpec, ivSpec);
        byte[] cipherText = cipher.doFinal(paddedData);

        // IV + 密文拼接
        byte[] result = new byte[iv.length + cipherText.length];
        System.arraycopy(iv, 0, result, 0, iv.length);
        System.arraycopy(cipherText, 0, result, iv.length, cipherText.length);
        return result;
    }

    /**
     * AES-CBC 解密
     * input: IV(16) + cipherText
     */
    public static byte[] aesCbcDecrypt(byte[] ivPlusCipher, String keyStr) throws Exception {
        if (ivPlusCipher.length < AES_BLOCK_SIZE) {
            throw new IllegalArgumentException("密文长度不足");
        }
        byte[] key = keyStr.getBytes(StandardCharsets.UTF_8);
        SecretKeySpec keySpec = new SecretKeySpec(key, "AES");
        Cipher cipher = Cipher.getInstance("AES/CBC/NoPadding");

        byte[] iv = new byte[AES_BLOCK_SIZE];
        System.arraycopy(ivPlusCipher, 0, iv, 0, AES_BLOCK_SIZE);
        int cipherLen = ivPlusCipher.length - AES_BLOCK_SIZE;
        byte[] cipherText = new byte[cipherLen];
        System.arraycopy(ivPlusCipher, AES_BLOCK_SIZE, cipherText, 0, cipherLen);

        IvParameterSpec ivSpec = new IvParameterSpec(iv);
        cipher.init(Cipher.DECRYPT_MODE, keySpec, ivSpec);
        byte[] paddedPlain = cipher.doFinal(cipherText);

        return pkcs7Unpad(paddedPlain, AES_BLOCK_SIZE);
    }

    // PKCS7填充，与Go pkcs7Pad完全一致
    public static byte[] pkcs7Pad(byte[] data, int blockSize) {
        int padding = blockSize - (data.length % blockSize);
        byte[] padded = new byte[data.length + padding];
        System.arraycopy(data, 0, padded, 0, data.length);
        for (int i = data.length; i < padded.length; i++) {
            padded[i] = (byte) padding;
        }
        return padded;
    }

    // PKCS7去填充，与Go pkcs7Unpad完全一致
    public static byte[] pkcs7Unpad(byte[] data, int blockSize) throws Exception {
        if (data.length == 0 || data.length % blockSize != 0) {
            throw new Exception("数据长度非法");
        }
        int padding = data[data.length - 1] & 0xFF;
        if (padding <= 0 || padding > blockSize) {
            throw new Exception("无效填充值");
        }
        for (int i = data.length - padding; i < data.length; i++) {
            if ((data[i] & 0xFF) != padding) {
                throw new Exception("填充内容错误");
            }
        }
        byte[] plain = new byte[data.length - padding];
        System.arraycopy(data, 0, plain, 0, plain.length);
        return plain;
    }

    /**
     * POST 请求：发送 raw binary，Content-Type=application/octet-stream
     * 返回接口响应raw二进制
     */
    public static byte[] doPostBinary(String urlStr, byte[] bodyBytes) throws Exception {
        URL url = new URL(urlStr);
        HttpURLConnection conn = (HttpURLConnection) url.openConnection();
        conn.setRequestMethod("POST");
        conn.setRequestProperty("Content-Type", "application/octet-stream");
        conn.setDoOutput(true);
        conn.setConnectTimeout(5000);
        conn.setReadTimeout(10000);

        // 写入二进制body
        try (OutputStream os = conn.getOutputStream()) {
            os.write(bodyBytes);
        }

        int respCode = conn.getResponseCode();
        if (respCode != 200) {
            // 读取错误流
            InputStream errStream = conn.getErrorStream();
            if (errStream != null) {
                ByteArrayOutputStream bos = new ByteArrayOutputStream();
                byte[] buf = new byte[1024];
                int len;
                while ((len = errStream.read(buf)) != -1) {
                    bos.write(buf, 0, len);
                }
                System.err.println("响应错误内容：" + new String(bos.toByteArray(), StandardCharsets.UTF_8));
            }
            throw new RuntimeException("http请求失败，httpCode=" + respCode);
        }

        // 读取响应二进制
        try (InputStream is = conn.getInputStream()) {
            ByteArrayOutputStream bos = new ByteArrayOutputStream();
            byte[] buffer = new byte[1024];
            int len;
            while ((len = is.read(buffer)) != -1) {
                bos.write(buffer, 0, len);
            }
            return bos.toByteArray();
        } finally {
            conn.disconnect();
        }
    }
}

